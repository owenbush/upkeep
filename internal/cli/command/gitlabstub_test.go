package command

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/owenbush/upkeep/internal/gitlab"
)

// aGitlabStub is a GitLab that answers exactly the paths a test scripts, and
// fails loudly on anything else.
//
// A real HTTP server rather than a faked client, because what these tests are
// about is the command's flow *through* the client — which requests it makes,
// in what order, and what it does with the answers — and a faked client would
// let a command ask for something nobody noticed.
func aGitlabStub(t *testing.T, answers map[string]string) (*httptest.Server, *gitlab.Client) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, scripted := answers[r.URL.EscapedPath()]
		if !scripted {
			// 404 rather than a test failure: several of these paths are ones
			// the client asks about and tolerates the absence of, and failing
			// here would make "did not answer" impossible to script.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))

			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	t.Cleanup(server.Close)

	return server, gitlab.NewClient(nil, "", server.URL+"/api/v4", server.URL)
}
