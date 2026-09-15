package drupal

import (
	"fmt"
	"net/url"
	"strings"
)

// User is a drupal.org account, resolved from the uid an attachment records as
// its owner.
//
// It exists for one job: naming the person whose work a patch is, in the
// commit that carries that work into a merge request. drupal.org allocates
// credit through its own issue-credit system rather than through git
// authorship, and the API offers a username and a profile URL and no email at
// all — so this deliberately carries no address. A `git commit --author` line
// assembled from a synthesised address would be a claim about identity that
// nothing here can support.
type User struct {
	UID        int
	Name       string
	ProfileURL string
}

// userFrom narrows an account payload. It reports false when the payload does
// not describe a usable account, rather than returning a half-built one.
func userFrom(data map[string]any) (User, bool) {
	uid, hasUID := intField(data, "uid")
	name := stringField(data, "name")
	if !hasUID || uid < 1 || name == "" {
		return User{}, false
	}

	profileURL := stringField(data, "url")
	if profileURL == "" {
		profileURL = profileURLFor(name)
	}

	return User{UID: uid, Name: name, ProfileURL: profileURL}, true
}

// profileURLFor is drupal.org's own profile path, used only when the payload
// omits url — which the account resource normally supplies.
func profileURLFor(name string) string {
	return "https://www.drupal.org/u/" + url.PathEscape(strings.ToLower(strings.ReplaceAll(name, " ", "-")))
}

// User is the account behind a uid — the one extra request a promoted patch
// costs, made once for the patch being promoted rather than per attachment on
// a listing.
//
// An unreadable account reports false and is warned about: a commit that
// silently dropped its attribution would be exactly the misappropriation the
// attribution exists to prevent.
func (c *Client) User(uid int) (User, bool) {
	if uid < 1 {
		return User{}, false
	}

	data, ok := c.getJSON(
		fmt.Sprintf("%s/user/%d.json", c.apiBase, uid),
		fmt.Sprintf("drupal.org account %d", uid),
	)
	if !ok || len(data) == 0 {
		return User{}, false
	}

	return userFrom(data)
}
