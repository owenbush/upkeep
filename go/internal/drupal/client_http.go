package drupal

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// getJSON fetches and decodes, recording a warning and answering false when it
// cannot. Nothing here returns an error: the caller renders warnings, and a
// failed request means less data rather than a failed command.
func (c *Client) getJSON(url, what string) (map[string]any, bool) {
	return c.getJSONWithRetry(url, what, true)
}

func (c *Client) getJSONWithRetry(url, what string, mayRetry bool) (map[string]any, bool) {
	response, err := c.http.Get(url)
	if err != nil {
		c.warn("Could not read %s: %v", what, err)

		return nil, false
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusTooManyRequests && mayRetry {
		wait, ok := retryAfter(response.Header.Get("Retry-After"))
		if !ok {
			c.warn(
				"drupal.org is throttling requests for %s and asked for a longer wait than %s; skipped.",
				what, maxRetryAfter,
			)

			return nil, false
		}
		io.Copy(io.Discard, response.Body)
		c.sleep(wait)

		return c.getJSONWithRetry(url, what, false)
	}

	if response.StatusCode != http.StatusOK {
		c.warn("Could not read %s: HTTP %d.", what, response.StatusCode)

		return nil, false
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		c.warn("Could not read %s: %v", what, err)

		return nil, false
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		c.warn("Could not read %s: the response was not JSON.", what)

		return nil, false
	}

	return data, true
}

// retryAfter reads the header, and reports false when the wait is longer than
// upkeep is willing to sit through.
func retryAfter(header string) (time.Duration, bool) {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		return time.Second, true // unparseable: wait the default, once
	}

	wait := time.Duration(seconds) * time.Second
	if wait > maxRetryAfter {
		return 0, false
	}
	if wait == 0 {
		wait = time.Second
	}

	return wait, true
}

// prefetchAttachments resolves every attachment reference these entries carry,
// concurrently.
//
// api-d7 returns an attachment as a bare reference — {"file":{"uri":…,"id":…}}
// — with no name, so every file costs a request of its own before an issue can
// say what it carries. drupal.org's origin is slow when its Varnish cache is
// cold: ~530ms per request against ~20ms warm, which serialised took ~52s for
// a busy module.
func (c *Client) prefetchAttachments(entries []map[string]any) {
	var wanted []int
	seen := map[int]bool{}

	for _, entry := range entries {
		for _, fid := range attachmentIDs(entry) {
			c.mu.Lock()
			_, known := c.files[fid]
			c.mu.Unlock()
			if known || seen[fid] {
				continue
			}
			seen[fid] = true
			wanted = append(wanted, fid)
		}
	}
	if len(wanted) == 0 {
		return
	}

	// Bounded, because api-d7 publishes no quota to stay inside and
	// politeness is the only available policy.
	var wg sync.WaitGroup
	slots := make(chan struct{}, MaxConcurrent)
	for _, fid := range wanted {
		wg.Add(1)
		go func(fid int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			c.fetchFile(fid)
		}(fid)
	}
	wg.Wait()
}

func (c *Client) fetchFile(fid int) {
	data, ok := c.getJSON(
		fmt.Sprintf("%s/file/%d.json", c.apiBase, fid),
		fmt.Sprintf("attachment %d", fid),
	)
	if !ok {
		// Recorded as absent rather than retried on every issue that
		// references it. The warning is already out.
		c.mu.Lock()
		c.files[fid] = IssueFile{}
		c.mu.Unlock()

		return
	}

	file := IssueFile{
		Name:      stringField(data, "name"),
		URL:       stringField(data, "url"),
		Size:      int64(intOr(data, "filesize")),
		Timestamp: int64(intOr(data, "timestamp")),
	}
	if file.Name == "" {
		// The URL is the only other place a name lives.
		file.Name = baseName(file.URL)
	}
	if owner, ok := nestedInt(data, "owner", "id"); ok {
		file.OwnerUID = owner
	}

	c.mu.Lock()
	c.files[fid] = file
	c.mu.Unlock()
}
