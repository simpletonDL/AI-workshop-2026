//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOrgAPI is a GitHub API that lists real public repositories as the
// organization "fixture-org", so the scan clones live repositories without
// depending on the API rate limit of the CI runner.
func fakeOrgAPI(t *testing.T, calls *atomic.Int32) string {
	t.Helper()
	repos := []map[string]any{
		{"html_url": repoClaudeOnly, "size": 100},
		{"html_url": repoMultiAgent, "size": 100},
		{"html_url": "https://github.com/simpletonDL/no-such-repo-atlas", "size": 0}, // empty: not cloned
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/fixture-org/repos" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_ = json.NewEncoder(w).Encode(repos)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestServeScansOrganization(t *testing.T) {
	var calls atomic.Int32
	var logs syncBuffer
	base, _ := startServeCmd(t, &logs, "--github-api", fakeOrgAPI(t, &calls))

	code, body := fetchQuery(t, base, url.Values{"org": {"fixture-org"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		"<h2>commit</h2>", "<h2>fh</h2>", "<h2>review</h2>",
		repoClaudeOnly + " · .claude/skills/commit/SKILL.md",
		repoMultiAgent + " · .cursor/skills/fh/SKILL.md",
		"in 2 of 3 repositories",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if strings.Contains(body, "could not be scanned") {
		t.Error("the empty repository was cloned")
	}

	// A repeated scan reuses the listing and the cloned repositories.
	if code, _ := fetchQuery(t, base, url.Values{"org": {"fixture-org"}}); code != http.StatusOK {
		t.Fatalf("second scan: status = %d", code)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("GitHub API called %d times, want 1", n)
	}
	if n := strings.Count(logs.String(), "repo cache hit"); n != 2 {
		t.Errorf("%d cache hits on the second scan, want 2; logs:\n%s", n, logs.String())
	}
}
