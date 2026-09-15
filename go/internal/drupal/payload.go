package drupal

import (
	"path"
	"strconv"
	"strings"
)

// api-d7 is a Drupal 7 Services endpoint, and it is loose about types: a node
// id may arrive as a number or as a string, a missing field may be absent,
// null, or an empty array. These readers answer "what did it actually say",
// and a field that is not there is simply not there rather than a zero that
// looks like an answer.

func stringField(data map[string]any, key string) string {
	switch value := data[key].(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return ""
	}
}

func intField(data map[string]any, key string) (int, bool) {
	switch value := data[key].(type) {
	case float64:
		return int(value), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))

		return n, err == nil
	default:
		return 0, false
	}
}

func intOr(data map[string]any, key string) int {
	n, _ := intField(data, key)

	return n
}

func nestedInt(data map[string]any, keys ...string) (int, bool) {
	current := data
	for i, key := range keys {
		if i == len(keys)-1 {
			return intField(current, key)
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			return 0, false
		}
		current = next
	}

	return 0, false
}

// listOf reads the "list" envelope every api-d7 listing comes in.
func listOf(data map[string]any) []map[string]any {
	raw, ok := data["list"].([]any)
	if !ok {
		return nil
	}

	entries := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if entry, ok := item.(map[string]any); ok {
			entries = append(entries, entry)
		}
	}

	return entries
}

// attachmentIDs reads the file ids an issue payload references.
//
// Attachments arrive as bare references — {"file":{"uri":…,"id":…}} — with no
// name and no URL, which is why each one costs a request of its own.
func attachmentIDs(entry map[string]any) []int {
	raw, ok := entry["field_issue_files"].([]any)
	if !ok {
		return nil
	}

	var ids []int
	for _, item := range raw {
		wrapper, ok := item.(map[string]any)
		if !ok {
			continue
		}
		file, ok := wrapper["file"].(map[string]any)
		if !ok {
			continue
		}
		if id, present := intField(file, "id"); present {
			ids = append(ids, id)
		}
	}

	return ids
}

// issueFrom builds an issue from a node payload, attaching the files already
// resolved by prefetchAttachments.
func (c *Client) issueFrom(entry map[string]any) (Issue, bool) {
	nid, ok := intField(entry, "nid")
	if !ok {
		return Issue{}, false
	}

	status, hasStatus := intField(entry, "field_issue_status")
	if !hasStatus {
		return Issue{}, false
	}

	issue := Issue{
		Nid:       nid,
		Title:     stringField(entry, "title"),
		Status:    IssueStatus(status),
		URL:       stringField(entry, "url"),
		Priority:  intOr(entry, "field_issue_priority"),
		Version:   stringField(entry, "field_issue_version"),
		Component: stringField(entry, "field_issue_component"),
		Category:  stringField(entry, "field_issue_category"),
	}
	if issue.URL == "" {
		issue.URL = IssueURL(nid)
	}
	if project, ok := entry["field_project"].(map[string]any); ok {
		issue.Project = stringField(project, "machine_name")
	}

	c.mu.Lock()
	for _, fid := range attachmentIDs(entry) {
		if file, known := c.files[fid]; known && file.Name != "" {
			issue.Files = append(issue.Files, file)
		}
	}
	c.mu.Unlock()

	return issue, true
}

func baseName(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	name := path.Base(rawURL)
	if name == "." || name == "/" {
		return ""
	}

	return name
}
