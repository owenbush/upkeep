package patches

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenbush/upkeep/internal/drupal"
)

const realPatch = "diff --git a/pathauto.info.yml b/pathauto.info.yml\n" +
	"--- a/pathauto.info.yml\n+++ b/pathauto.info.yml\n@@ -1 +1 @@\n-core: 10\n+core: 11\n"

// serving stands up a server answering every request with one body.
func serving(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server
}

func TestAPatchLandsUnderTheIssuesOwnDirectory(t *testing.T) {
	server := serving(t, 200, realPatch)
	cache := t.TempDir()

	path, err := NewFetcher(server.Client(), cache).Fetch(3597808, "3597808-9-d11.patch", server.URL+"/x.patch")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	want := filepath.Join(cache, "3597808", "3597808-9-d11.patch")
	if path != want {
		t.Errorf("landed at %q, want %q", path, want)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(contents) != realPatch {
		t.Error("the file on disk is not what was served")
	}
}

// The name arrives from a remote API, so it is treated as hostile input: only
// the basename survives, and only from a conservative character set.
func TestAFilenameCannotEscapeTheCacheDirectory(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"../../etc/passwd", "passwd"},
		{"..%2F..%2Fpasswd", "_2F.._2Fpasswd"},
		{`..\..\windows\system32`, "system32"},
		{"/absolute/path.patch", "path.patch"},
		{"...", "patch.patch"},
		{"", "patch.patch"},
		{"..", "patch.patch"},
		{".hidden", "hidden"},
		{"space and 'quotes'.patch", "space_and__quotes_.patch"},
		{"3597808-9-d11.patch", "3597808-9-d11.patch"},
	} {
		if got := SafeName(tc.name); got != tc.want {
			t.Errorf("SafeName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestATraversingNameStillLandsInsideTheCache(t *testing.T) {
	server := serving(t, 200, realPatch)
	cache := t.TempDir()

	path, err := NewFetcher(server.Client(), cache).Fetch(1, "../../escape.patch", server.URL+"/x")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.HasPrefix(path, cache+"/") {
		t.Fatalf("landed at %q, outside %q", path, cache)
	}
	if strings.Contains(path, "..") {
		t.Errorf("landed at %q", path)
	}
}

// file:// and php:// would turn "download this patch" into "read this local
// path through an HTTP client" — a different operation than the one being
// authorised.
func TestOnlyHttpUrlsAreFetched(t *testing.T) {
	fetcher := NewFetcher(nil, t.TempDir())

	for _, url := range []string{
		"file:///etc/passwd",
		"php://filter/read=convert.base64-encode/resource=/etc/passwd",
		"data:text/plain,diff --git a b",
		"ftp://example.test/x.patch",
		"/etc/passwd",
		"",
	} {
		if _, err := fetcher.Fetch(1, "x.patch", url); err == nil {
			t.Errorf("%q was fetched", url)
		} else if !strings.Contains(err.Error(), "only http and https") {
			t.Errorf("%q refused with %q", url, err)
		}
	}
}

// The usual cause is a redirect to an HTML login page, which would otherwise
// surface as an inscrutable git error about a corrupt patch.
func TestABodyThatIsNotADiffIsRefusedBeforeGitSeesIt(t *testing.T) {
	server := serving(t, 200, "<html><body>Please sign in</body></html>")

	_, err := NewFetcher(server.Client(), t.TempDir()).Fetch(1, "x.patch", server.URL+"/x")
	if err == nil {
		t.Fatal("an HTML page was accepted as a patch")
	}
	if !strings.Contains(err.Error(), "no diff header") {
		t.Errorf("error %q does not say what is wrong", err)
	}
	if !strings.Contains(err.Error(), "redirected") {
		t.Errorf("error %q does not name the usual cause", err)
	}
}

func TestAnEmptyBodyIsRefused(t *testing.T) {
	server := serving(t, 200, "   \n\n  ")

	_, err := NewFetcher(server.Client(), t.TempDir()).Fetch(1, "x.patch", server.URL+"/x")
	if err == nil {
		t.Fatal("an empty body was accepted")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error %q", err)
	}
}

// Every opening a unified diff can have.
func TestEveryDiffOpeningIsAccepted(t *testing.T) {
	for _, body := range []string{
		"diff --git a/x b/x\n",
		"--- a/x\n+++ b/x\n",
		"Index: x\n",
		"index 1234567..89abcde 100644\n",
		"Some preamble text\ndiff --git a/x b/x\n",
		"A comment from the issue\n--- a/x\n",
	} {
		server := serving(t, 200, body)
		if _, err := NewFetcher(server.Client(), t.TempDir()).Fetch(1, "x.patch", server.URL+"/x"); err != nil {
			t.Errorf("%q was refused: %v", strings.SplitN(body, "\n", 2)[0], err)
		}
	}
}

func TestANonOkStatusIsReportedWithIt(t *testing.T) {
	server := serving(t, 404, "not here")

	_, err := NewFetcher(server.Client(), t.TempDir()).Fetch(1, "x.patch", server.URL+"/x")
	if err == nil {
		t.Fatal("a 404 was accepted")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error %q", err)
	}
}

// The cap exists so a wrong URL cannot fill the disk before anyone notices.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	server := serving(t, 200, "diff --git a/x b/x\n"+strings.Repeat("+", MaxBytes))

	_, err := NewFetcher(server.Client(), t.TempDir()).Fetch(1, "x.patch", server.URL+"/x")
	if err == nil {
		t.Fatal("an oversized body was accepted")
	}
	if !strings.Contains(err.Error(), "patch limit") {
		t.Errorf("error %q", err)
	}
}

// And nothing is left on disk from a refused download.
func TestARefusedDownloadWritesNothing(t *testing.T) {
	cache := t.TempDir()
	server := serving(t, 200, "<html>nope</html>")

	if _, err := NewFetcher(server.Client(), cache).Fetch(3597808, "x.patch", server.URL+"/x"); err == nil {
		t.Fatal("accepted")
	}

	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("left %d entries behind", len(entries))
	}
}

// The commit says whose work it is, in the place a reader of the branch will
// actually look.
func TestThePromotedCommitNamesThePatchAuthor(t *testing.T) {
	author := drupal.User{UID: 12345, Name: "somebody", ProfileURL: "https://www.drupal.org/u/somebody"}
	attribution := AttributionFor(
		drupal.Issue{Nid: 3597808, Title: "Drupal 12 compatibility"},
		patch("3597808-9-d11.patch", 100, 4200),
		&author,
		"owenbush",
	)

	if got := attribution.Subject(); got != "Issue #3597808 by somebody: Drupal 12 compatibility" {
		t.Errorf("subject %q", got)
	}

	message := attribution.Message()
	for _, want := range []string{
		"Patch-author: somebody <https://www.drupal.org/u/somebody>",
		"Patch-file: 3597808-9-d11.patch",
		"Patch-source: https://www.drupal.org/files/issues/3597808-9-d11.patch",
		"Issue: https://www.drupal.org/node/3597808",
		"Promoted-by: owenbush",
		"not authoring it",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message does not carry %q:\n%s", want, message)
		}
	}
}

// Both an --author and a Co-authored-by: want an email address, and drupal.org
// publishes none. Synthesising one would put a claim about somebody else's
// identity into permanent history.
func TestNoEmailAddressIsEverSynthesised(t *testing.T) {
	author := drupal.User{UID: 12345, Name: "somebody", ProfileURL: "https://www.drupal.org/u/somebody"}
	message := AttributionFor(
		drupal.Issue{Nid: 1, Title: "t"}, patch("x.patch", 0, 0), &author, "owenbush",
	).Message()

	for _, forbidden := range []string{"Co-authored-by", "@drupal.org", "@users.noreply"} {
		if strings.Contains(message, forbidden) {
			t.Errorf("message carries %q:\n%s", forbidden, message)
		}
	}
	// The one angle-bracketed value is the profile URL, which is a real
	// published fact.
	if strings.Count(message, "<") != 1 {
		t.Errorf("message carries more than the profile URL in brackets:\n%s", message)
	}
}

// Silence there is the exact misappropriation the attribution exists to
// prevent.
func TestAnUnresolvableAuthorIsSaidRatherThanOmitted(t *testing.T) {
	attribution := AttributionFor(
		drupal.Issue{Nid: 1, Title: "Drupal 12 compatibility"}, patch("x.patch", 0, 0), nil, "owenbush",
	)

	if got := attribution.Subject(); got != "Issue #1: Drupal 12 compatibility" {
		t.Errorf("subject %q invents a credit", got)
	}

	message := attribution.Message()
	if !strings.Contains(message, "the patch author's work") {
		t.Errorf("message does not say the work is not the promoter's:\n%s", message)
	}
	if !strings.Contains(message, "credit") {
		t.Errorf("message does not say what to do about it:\n%s", message)
	}
	if strings.Contains(message, "Patch-author:") {
		t.Errorf("message names an author it does not have:\n%s", message)
	}
}

// The subject is a string ExtractOwningIssue parses, so the resulting merge
// request pairs with its issue the same way every other one does.
func TestTheSubjectPairsWithItsIssue(t *testing.T) {
	author := drupal.User{UID: 1, Name: "somebody", ProfileURL: "u"}

	for _, attribution := range []Attribution{
		AttributionFor(drupal.Issue{Nid: 3597808, Title: "t"}, patch("x.patch", 0, 0), &author, ""),
		AttributionFor(drupal.Issue{Nid: 3597808, Title: "t"}, patch("x.patch", 0, 0), nil, ""),
	} {
		nid, found := drupal.ExtractOwningIssue(attribution.Subject(), "", "")
		if !found || nid != 3597808 {
			t.Errorf("subject %q does not claim its issue (%d, %v)", attribution.Subject(), nid, found)
		}
	}
}

func TestAnUnrecordedPromoterIsLeftOut(t *testing.T) {
	message := AttributionFor(
		drupal.Issue{Nid: 1, Title: "t"}, patch("x.patch", 0, 0), nil, "",
	).Message()

	if strings.Contains(message, "Promoted-by:") {
		t.Errorf("message carries an empty trailer:\n%s", message)
	}
	if !strings.HasSuffix(message, "\n") {
		t.Errorf("message does not end with a newline: %q", fmt.Sprintf("%q", message))
	}
}
