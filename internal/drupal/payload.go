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
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
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
	// A payload decoded from JSON only ever holds float64, but one *built* in
	// this process — a model rendered back through ToAPIMap, which is what a
	// snapshot does before it is serialised — holds real ints. PHP's is_int
	// covers both without anyone thinking about it.
	case int:
		return value, true
	case int64:
		return int(value), true
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

// attachmentEntries is the attachment list out of an issue payload.
//
// Drupal 7 wraps a field in a language envelope ({"und": [...]}) on some
// endpoints and not on others, so both shapes are read.
func attachmentEntries(entry map[string]any) ([]any, bool) {
	switch attachments := entry["field_issue_files"].(type) {
	case []any:
		return attachments, true
	case map[string]any:
		if envelope, ok := attachments["und"].([]any); ok {
			return envelope, true
		}

		return nil, true
	default:
		return nil, false
	}
}

// attachmentIDs reads the file ids an issue payload still needs resolved.
//
// Attachments arrive as bare references — {"file":{"uri":…,"id":…}} — with no
// name and no URL, which is why each one costs a request of its own. An entry
// that already carries a name has been resolved (or came from a snapshot) and
// is skipped.
func attachmentIDs(entry map[string]any) []int {
	entries, present := attachmentEntries(entry)
	if !present {
		return nil
	}

	var ids []int
	for _, item := range entries {
		wrapper, ok := item.(map[string]any)
		if !ok {
			continue
		}
		file, ok := wrapper["file"].(map[string]any)
		if !ok {
			continue
		}
		if _, named := file["name"]; named {
			continue
		}
		if _, named := file["filename"]; named {
			continue
		}
		id, present := intField(file, "id")
		if !present {
			id, present = intField(file, "fid")
		}
		if present {
			ids = append(ids, id)
		}
	}

	return ids
}

// withResolvedFiles rewrites each bare attachment reference in an issue
// payload into the file detail already fetched for it.
//
// In the payload rather than beside it, because that payload is what the
// dashboard snapshot stores: the models are then built from one shape whether
// they came from the network or from a cache file.
//
// A file that could not be read stays a bare reference rather than becoming a
// half-built attachment — IssueFileFrom drops it, and the issue reports one
// attachment fewer instead of one nameless one.
func (c *Client) withResolvedFiles(data map[string]any) map[string]any {
	entries, present := attachmentEntries(data)
	if !present {
		return data
	}

	resolved := make([]any, 0, len(entries))
	for _, item := range entries {
		wrapper, ok := item.(map[string]any)
		if !ok {
			resolved = append(resolved, item)

			continue
		}
		resolved = append(resolved, c.resolveAttachment(wrapper))
	}

	// Copied rather than written through, so a caller's payload is not
	// mutated under it — PHP's arrays are values and get this for free.
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	if _, wasEnvelope := data["field_issue_files"].(map[string]any); wasEnvelope {
		out["field_issue_files"] = map[string]any{"und": resolved}
	} else {
		out["field_issue_files"] = resolved
	}

	return out
}

func (c *Client) resolveAttachment(entry map[string]any) map[string]any {
	file, ok := entry["file"].(map[string]any)
	if !ok {
		return entry
	}
	fid, present := intField(file, "id")
	if !present {
		fid, present = intField(file, "fid")
	}
	if !present {
		return entry
	}

	// prefetchAttachments has already visited every attachment this payload
	// references — successes and misses alike — so the memo holds an entry for
	// each; a nil detail is a recorded miss, not an unasked question.
	c.mu.Lock()
	detail := c.files[fid]
	c.mu.Unlock()
	if detail == nil {
		return entry
	}

	merged := make(map[string]any, len(entry))
	for key, value := range entry {
		merged[key] = value
	}
	merged["file"] = detail

	return merged
}

// IssueFrom builds an issue from a node payload, reading its attachments out
// of the payload itself.
//
// Payload-only on purpose. The client rewrites each bare attachment reference
// into the resolved file detail *in the payload* before this reads it, and the
// dashboard snapshot stores that rewritten payload — so a cached snapshot and
// a live fetch go through one path, and a snapshot written by either
// implementation is readable by the other.
//
// An issue without a usable node id, or with a status upkeep does not
// understand, is not an issue it can reason about: no partial model is built
// for it.
func IssueFrom(entry map[string]any) (Issue, bool) {
	nid, ok := intField(entry, "nid")
	if !ok {
		return Issue{}, false
	}

	status, hasStatus := intField(entry, "field_issue_status")
	if !hasStatus || !IssueStatus(status).Known() {
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
		Category:  categoryLabel(intOr(entry, "field_issue_category")),
		Files:     issueFilesFrom(entry),
	}
	if issue.URL == "" {
		issue.URL = IssueURL(nid)
	}
	if project, ok := entry["field_project"].(map[string]any); ok {
		issue.Project = stringField(project, "machine_name")
	}

	return issue, true
}

func issueFilesFrom(entry map[string]any) []IssueFile {
	entries, present := attachmentEntries(entry)
	if !present {
		return nil
	}

	var files []IssueFile
	for _, item := range entries {
		fields, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if file, built := IssueFileFrom(fields); built {
			files = append(files, file)
		}
	}

	return files
}

// IssueFileFrom narrows one attachment entry, which the API returns either
// wrapped in a "file" envelope or flat.
//
// Without a name and a URL there is nothing to download or classify, so such
// an entry is no file at all — an issue reports one attachment fewer rather
// than one nameless one.
func IssueFileFrom(entry map[string]any) (IssueFile, bool) {
	fields := entry
	if wrapped, ok := entry["file"].(map[string]any); ok {
		fields = wrapped
	}

	name := stringField(fields, "filename")
	if name == "" {
		name = stringField(fields, "name")
	}
	url := stringField(fields, "url")
	if name == "" || url == "" {
		return IssueFile{}, false
	}

	file := IssueFile{
		Name:      name,
		URL:       url,
		Size:      int64(intOr(fields, "filesize")),
		Timestamp: int64(intOr(fields, "timestamp")),
	}
	// "owner" is the file resource's shape; "uid" is accepted because a stored
	// snapshot flattens it.
	if owner, ok := nestedInt(fields, "owner", "id"); ok {
		file.OwnerUID = owner
	} else if uid, ok := intField(fields, "uid"); ok {
		file.OwnerUID = uid
	}

	return file, true
}

// categoryLabel maps drupal.org's category id to what the issue page shows.
// An id upkeep does not know reads as nothing rather than as a number.
func categoryLabel(id int) string {
	switch id {
	case 1:
		return "Bug report"
	case 2:
		return "Task"
	case 3:
		return "Feature request"
	case 4:
		return "Support request"
	case 5:
		return "Plan"
	default:
		return ""
	}
}

// categoryID is the inverse, for writing a payload back out.
func categoryID(label string) any {
	for id := 1; id <= 5; id++ {
		if categoryLabel(id) == label {
			return id
		}
	}

	return nil
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
