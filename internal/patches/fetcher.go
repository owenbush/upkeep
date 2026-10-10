package patches

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/owenbush/upkeep/internal/filesystem"
)

// MaxBytes bounds a download.
//
// Patches are text diffs; the largest thing anyone legitimately attaches to a
// drupal.org issue is orders of magnitude under this. The cap exists so a
// wrong URL cannot fill the disk before anyone notices.
const MaxBytes = 8 * 1024 * 1024

const (
	// fetchIdleTimeout is the gap allowed before a response begins.
	fetchIdleTimeout = 30 * time.Second
	// fetchMaxDuration is the total cap on one download.
	fetchMaxDuration = 120 * time.Second
)

// Fetcher downloads a patch file into the cockpit and hands back its local
// path.
//
// Kept out of the adapter deliberately: the adapter's job stops at engine
// mechanics, and by the time a patch reaches it the file must already be on
// disk and vouched for. Everything that could go wrong with an untrusted URL
// and an untrusted filename is therefore decided here, once.
type Fetcher struct {
	http     *http.Client
	cacheDir string
}

// NewFetcher stores downloads under cacheDir. A nil client takes one bounded
// the way every other outbound request in this tool is.
func NewFetcher(client *http.Client, cacheDir string) *Fetcher {
	if client == nil {
		client = &http.Client{
			Timeout: fetchMaxDuration,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: fetchIdleTimeout,
			},
		}
	}

	return &Fetcher{http: client, cacheDir: cacheDir}
}

// Fetch downloads patchURL and stores it as name under the cache directory,
// keyed by issue. It returns the absolute local path.
func (f *Fetcher) Fetch(issueNid int, name, patchURL string) (string, error) {
	if err := assertFetchableURL(patchURL); err != nil {
		return "", err
	}

	target := filepath.Join(strings.TrimRight(f.cacheDir, "/"), strconv.Itoa(issueNid), SafeName(name))

	body, err := f.download(patchURL)
	if err != nil {
		return "", err
	}
	if err := assertLooksLikeAPatch(body, name); err != nil {
		return "", err
	}

	if err := filesystem.EnsureDirectory(filepath.Dir(target), filesystem.ModeSharedDir); err != nil {
		return "", err
	}
	if err := filesystem.Write(target, body, filesystem.ModeShared); err != nil {
		return "", err
	}

	return target, nil
}

// SafeName is the filename reduced to something that cannot escape the cache
// directory.
//
// The name arrives from a remote API, so it is treated as hostile input: only
// the basename survives, and only from a conservative character set.
func SafeName(name string) string {
	// Backslashes first: a Windows-style path is still a path, and path.Base
	// would keep the whole thing as one segment.
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))
	base = replaceUnsafeBytes(base)
	base = strings.TrimLeft(base, ".")
	if base == "" {
		return "patch.patch"
	}

	return base
}

// replaceUnsafeBytes maps everything outside [A-Za-z0-9._-] to an underscore,
// one underscore **per byte**.
//
// Byte-wise on purpose. PHP's preg_replace runs without the /u modifier, so a
// two-byte "é" becomes two underscores and a three-byte CJK character becomes
// three. Go's regexp works on runes and would make one apiece — safe either
// way, but the two implementations cache the downloaded patch under the name
// this produces, so a difference here puts the same patch at two paths.
func replaceUnsafeBytes(value string) string {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		safe := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == '-'
		if safe {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}

	return string(out)
}

// assertFetchableURL allows only http(s).
//
// A patch source is a URL someone typed, and file://, php:// and friends would
// turn "download this patch" into "read this local path through an HTTP
// client" — a different operation than the one being authorised.
func assertFetchableURL(raw string) error {
	parsed, err := url.Parse(raw)
	scheme := ""
	if err == nil {
		scheme = strings.ToLower(parsed.Scheme)
	}
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("refusing to fetch %q: only http and https patch URLs are supported", raw)
	}

	return nil
}

// patchMarkers are the openings a unified diff can have.
var patchMarkers = []string{"diff --git ", "--- ", "Index: ", "index "}

// assertLooksLikeAPatch refuses a body that is not a unified diff before it
// can reach `git apply`.
//
// The usual cause is not an attack but a redirect to an HTML login or
// interstitial page, which would otherwise surface as an inscrutable git error
// about a corrupt patch.
func assertLooksLikeAPatch(body []byte, name string) error {
	text := string(body)
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("downloaded patch %q is empty", name)
	}

	for _, marker := range patchMarkers {
		if strings.Contains(text, "\n"+marker) || strings.HasPrefix(text, marker) {
			return nil
		}
	}

	return fmt.Errorf(
		"downloaded %q does not look like a patch (no diff header found). The URL may have redirected to "+
			"an HTML page rather than the file", name,
	)
}

func (f *Fetcher) download(patchURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchMaxDuration)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, patchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading %s failed: %w", patchURL, err)
	}
	request.Header.Set("Accept", "text/plain, */*")

	response, err := f.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("downloading %s failed: %w", patchURL, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s failed with HTTP %d", patchURL, response.StatusCode)
	}

	// One byte past the cap, so an oversized body is refused without reading
	// all of it — the PHP reads the whole thing and then measures, which on a
	// wrong URL is the disk-filling this exists to prevent.
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s failed: %w", patchURL, err)
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf(
			"refusing %s: the body exceeds the %d-byte patch limit", patchURL, MaxBytes,
		)
	}

	return body, nil
}
