package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub serves GET /users/<org>/repos like the GitHub API: pages,
// Link headers, ETags and 304 answers.
type fakeGitHub struct {
	mu       sync.Mutex
	repos    map[string][]orgRepo // by organization
	status   int                  // if set, every request fails with it
	header   http.Header          // sent with status
	requests []string             // "<path>?<query> <status>"
	auth     string
	url      string // of the server, set by newTestOrgs
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = r.Header.Get("Authorization")
	code := f.serve(w, r)
	f.requests = append(f.requests, fmt.Sprintf("%s?%s %d", r.URL.Path, r.URL.RawQuery, code))
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) int {
	if f.status != 0 {
		for k, v := range f.header {
			w.Header()[k] = v
		}
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"message":"Server Error"}`))
		return f.status
	}
	org, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/repos")
	repos, found := f.repos[strings.ToLower(org)]
	if !ok || !found {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return http.StatusNotFound
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	from := min((page-1)*perPage, len(repos))
	to := min(from+perPage, len(repos))
	etag := fmt.Sprintf(`W/"%s-%d-%d"`, strings.ToLower(org), page, len(repos))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return http.StatusNotModified
	}
	w.Header().Set("ETag", etag)
	if to < len(repos) {
		w.Header().Set("Link", fmt.Sprintf(`<%s?page=%d>; rel="next"`, r.URL.Path, page+1))
	}
	_ = json.NewEncoder(w).Encode(repos[from:to])
	return http.StatusOK
}

// fail makes every request fail with status and header; 0 stops failing.
func (f *fakeGitHub) fail(status int, header http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.header = status, header
}

func (f *fakeGitHub) setRepos(org string, repos []orgRepo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[org] = repos
}

func (f *fakeGitHub) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func manyRepos(org string, n int) []orgRepo {
	repos := make([]orgRepo, n)
	for i := range repos {
		repos[i] = orgRepo{URL: fmt.Sprintf("https://github.com/%s/r%d", org, i), Size: 1}
	}
	return repos
}

func newTestOrgs(t *testing.T, api *fakeGitHub, token string, max int, ttl time.Duration) (*githubOrgs, *fakeClock) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	api.url = srv.URL
	orgs, err := newGitHubOrgs(srv.URL, token, max, ttl)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	orgs.now = clock.now
	return orgs, clock
}

func TestParseOrg(t *testing.T) {
	orgs, err := newGitHubOrgs(defaultGitHubAPI, "", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"anthropics":                      "anthropics",
		"  my-org ":                       "my-org",
		"https://github.com/anthropics":   "anthropics",
		"https://GitHub.com/anthropics/":  "anthropics",
		"github.com/anthropics":           "anthropics",
		"http://github.com/anthropics":    "anthropics",
		"https://github.com/anthropics?x": "anthropics",
	} {
		if got, err := orgs.parseOrg(in); err != nil || got != want {
			t.Errorf("parseOrg(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "-org", "a_b", "../etc", "https://gitlab.com/org", "https://github.com/org/repo",
		"file:///etc", "org name", strings.Repeat("a", 40),
	} {
		if got, err := orgs.parseOrg(in); !errors.Is(err, errInvalidOrg) {
			t.Errorf("parseOrg(%q) = %q, %v; want an invalid organization error", in, got, err)
		}
	}

	// GitHub Enterprise: organizations are named by URLs of the API host.
	ghe, err := newGitHubOrgs("https://ghe.example.com/api/v3/", "", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ghe.parseOrg("https://ghe.example.com/team"); err != nil || got != "team" {
		t.Errorf("GHE parseOrg = %q, %v", got, err)
	}
	if ghe.api != "https://ghe.example.com/api/v3" {
		t.Errorf("api = %q", ghe.api)
	}
}

func TestGitHubOrgsListPages(t *testing.T) {
	api := &fakeGitHub{repos: map[string][]orgRepo{"big": manyRepos("big", 150), "small": manyRepos("small", 3)}}
	orgs, _ := newTestOrgs(t, api, "s3cret", 200, time.Minute)

	repos, truncated, err := orgs.list(context.Background(), "big")
	if err != nil || truncated || len(repos) != 150 || repos[149].URL != "https://github.com/big/r149" {
		t.Fatalf("list(big) = %d repos, truncated %v, %v", len(repos), truncated, err)
	}
	want := []string{
		"/users/big/repos?type=owner&sort=pushed&per_page=100&page=1 200",
		"/users/big/repos?type=owner&sort=pushed&per_page=100&page=2 200",
	}
	if got := api.calls(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if api.auth != "Bearer s3cret" {
		t.Errorf("Authorization = %q", api.auth)
	}

	if repos, truncated, err := orgs.list(context.Background(), "small"); err != nil || truncated || len(repos) != 3 {
		t.Errorf("list(small) = %d repos, truncated %v, %v", len(repos), truncated, err)
	}
}

func TestGitHubOrgsListTruncated(t *testing.T) {
	for _, tc := range []struct {
		max       int
		calls     int
		truncated bool
	}{{5, 1, true}, {7, 1, false}, {3, 1, true}} {
		api := &fakeGitHub{repos: map[string][]orgRepo{"org": manyRepos("org", 7)}}
		orgs, _ := newTestOrgs(t, api, "", tc.max, time.Minute)
		repos, truncated, err := orgs.list(context.Background(), "org")
		if err != nil || len(repos) != min(tc.max, 7) || truncated != tc.truncated || len(api.calls()) != tc.calls {
			t.Errorf("max %d: %d repos, truncated %v, %d calls, %v", tc.max, len(repos), truncated, len(api.calls()), err)
		}
	}
}

func TestGitHubOrgsListCachedAndRevalidated(t *testing.T) {
	api := &fakeGitHub{repos: map[string][]orgRepo{"org": manyRepos("org", 3)}}
	orgs, clock := newTestOrgs(t, api, "", 100, time.Minute)
	ctx := context.Background()

	for range 2 {
		if repos, _, err := orgs.list(ctx, "Org"); err != nil || len(repos) != 3 {
			t.Fatalf("list = %d repos, %v", len(repos), err)
		}
	}
	if got := api.calls(); len(got) != 1 {
		t.Errorf("listing within the TTL called the API again: %v", got)
	}

	// After the TTL the listing is revalidated with its ETag: 304, no quota used.
	clock.advance(2 * time.Minute)
	if repos, _, err := orgs.list(ctx, "org"); err != nil || len(repos) != 3 {
		t.Fatalf("revalidated list = %d repos, %v", len(repos), err)
	}
	if got := api.calls(); len(got) != 2 || !strings.HasSuffix(got[1], " 304") {
		t.Errorf("requests = %v, want a 304 revalidation", got)
	}

	// A changed organization is listed again.
	clock.advance(2 * time.Minute)
	api.setRepos("org", manyRepos("org", 4))
	if repos, _, err := orgs.list(ctx, "org"); err != nil || len(repos) != 4 {
		t.Errorf("changed list = %d repos, %v", len(repos), err)
	}

	// If the API fails, the earlier listing is used.
	clock.advance(2 * time.Minute)
	api.fail(http.StatusInternalServerError, nil)
	if repos, _, err := orgs.list(ctx, "org"); err != nil || len(repos) != 4 {
		t.Errorf("list on API failure = %d repos, %v; want the earlier listing", len(repos), err)
	}
}

func TestGitHubOrgsListErrors(t *testing.T) {
	ctx := context.Background()
	api := &fakeGitHub{repos: map[string][]orgRepo{}}
	orgs, _ := newTestOrgs(t, api, "", 100, time.Minute)
	if _, _, err := orgs.list(ctx, "nobody"); !errors.Is(err, errOrgNotFound) {
		t.Errorf("unknown org: err = %v", err)
	}

	api.fail(http.StatusForbidden, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1790847434"}})
	_, _, err := orgs.list(ctx, "org")
	if err == nil || !strings.Contains(err.Error(), "rate limit exceeded, resets at") || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("rate limit: err = %v", err)
	}

	api.fail(http.StatusInternalServerError, nil)
	if _, _, err := orgs.list(ctx, "org"); err == nil || !strings.Contains(err.Error(), "500 Internal Server Error: Server Error") {
		t.Errorf("server error: err = %v", err)
	}
}

// orgHandler serves the main page with the organization "acme": repositories
// a (skills deploy, review), b (empty, never cloned) and c (skill release).
func orgHandler(t *testing.T, max int) (http.Handler, *repoCheckout, *fakeGitHub) {
	t.Helper()
	co := twoRepos(t)
	co.dirs["https://github.com/acme/a"] = co.dirs["https://github.com/org/a"]
	co.dirs["https://github.com/acme/c"] = co.dirs["https://github.com/org/b"]
	api := &fakeGitHub{repos: map[string][]orgRepo{"acme": {
		{URL: "https://github.com/acme/a", Size: 10},
		{URL: "https://github.com/acme/b", Size: 0},
		{URL: "https://github.com/acme/c", Size: 5},
	}}}
	orgs, _ := newTestOrgs(t, api, "", max, time.Minute)
	return newServeHandler(co.checkout, withGitHub(orgs)), co, api
}

func TestServeScansOrganization(t *testing.T) {
	h, co, api := orgHandler(t, 100)
	// An organization URL is shown as its name.
	code, body := get(t, h, "/?org="+url.QueryEscape(api.url+"/acme/"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		"<h2>deploy</h2>", "<h2>review</h2>", "<h2>release</h2>",
		"3 skills found in 2 of 3 repositories",
		`https://github.com/acme/c · .claude/skills/release/SKILL.md`,
		`name="org" value="acme"`,
		"<title>acme organization · atlas</title>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if strings.Contains(body, "most recently pushed") {
		t.Error("page says the organization was truncated")
	}
	// The empty repository is not cloned.
	if got := strings.Join(co.calls, ","); got != "https://github.com/acme/a,https://github.com/acme/c" &&
		got != "https://github.com/acme/c,https://github.com/acme/a" {
		t.Errorf("cloned %s", got)
	}

	// Recorded in the history with the organization.
	_, body = get(t, h, "/")
	if !strings.Contains(body, `<a href="/?org=acme">`) ||
		!strings.Contains(body, `<span class="ref">organization</span>`) {
		t.Errorf("history has no organization entry:\n%s", body)
	}
}

func TestServeOrganizationWithRepos(t *testing.T) {
	h, co, _ := orgHandler(t, 100)
	co.dirs["https://github.com/acme/a.git"] = co.dirs["https://github.com/acme/a"]
	// Repository a is listed in the form and in the organization: cloned once.
	code, body := get(t, h, "/?"+rowsQuery("https://github.com/acme/a.git")+"&org=acme&filter=re")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	if !strings.Contains(body, "2 of 3 skills match") {
		t.Errorf("page has no filter summary:\n%s", body)
	}
	if len(co.calls) != 2 {
		t.Errorf("cloned %v, want a and c once each", co.calls)
	}
	// Clear filter keeps both the repositories and the organization; the
	// cluster link only the repositories.
	if !strings.Contains(body, `href="/?repo=https%3A%2F%2Fgithub.com%2Facme%2Fa.git&amp;org=acme">Clear filter`) {
		t.Errorf("no clear filter link with the organization:\n%s", body)
	}
	if !strings.Contains(body, `href="/cluster?repo=https%3A%2F%2Fgithub.com%2Facme%2Fa.git&amp;edit=1"`) {
		t.Errorf("no cluster link with the repositories")
	}
}

func TestServeOrganizationTruncated(t *testing.T) {
	h, _, _ := orgHandler(t, 2)
	_, body := get(t, h, "/?org=acme")
	if !strings.Contains(body, "Only the 2 most recently pushed repositories of acme were scanned.") {
		t.Errorf("page has no truncation note:\n%s", body)
	}
	if !strings.Contains(body, "2 skills found in 1 of 2 repositories") {
		t.Errorf("page has no summary of 2 repositories")
	}
}

func TestServeOrganizationErrors(t *testing.T) {
	h, co, api := orgHandler(t, 100)
	for _, tc := range []struct {
		org    string
		status int
		want   string
	}{
		{"https://gitlab.com/acme", http.StatusBadRequest, "use a"},
		{"nobody", http.StatusNotFound, "no 127.0.0.1"},
	} {
		code, body := get(t, h, "/?org="+tc.org)
		if code != tc.status || !strings.Contains(body, `<div class="message error" role="alert">`) || !strings.Contains(body, tc.want) {
			t.Errorf("org %q: status %d, body:\n%s", tc.org, code, body)
		}
	}
	if len(co.calls) != 0 {
		t.Errorf("cloned %v", co.calls)
	}

	api.fail(http.StatusBadGateway, nil)
	if code, body := get(t, h, "/?org=acme"); code != http.StatusBadGateway || !strings.Contains(body, "GitHub API: 502") {
		t.Errorf("API failure: status %d, body:\n%s", code, body)
	}

	// Editing the form without JavaScript keeps the organization and scans nothing.
	calls := len(api.calls())
	_, body := get(t, h, "/?org=acme&repo=&ref=&add=1")
	if !strings.Contains(body, `name="org" value="acme"`) || !strings.Contains(body, "<title>acme organization · atlas</title>") ||
		len(api.calls()) != calls {
		t.Errorf("edit: %d API calls, body:\n%s", len(api.calls()), body)
	}
}

func TestServeOrganizationWithoutRepos(t *testing.T) {
	api := &fakeGitHub{repos: map[string][]orgRepo{"empty": {}}}
	orgs, _ := newTestOrgs(t, api, "", 100, time.Minute)
	code, body := get(t, newServeHandler((&stubCheckout{}).checkout, withGitHub(orgs)), "/?org=empty")
	if code != http.StatusOK || !strings.Contains(body, "No skills found") {
		t.Errorf("status %d, body:\n%s", code, body)
	}
}

func TestScanReposLimitsParallelClones(t *testing.T) {
	var mu sync.Mutex
	running, peak := 0, 0
	co := func(ctx context.Context, source, ref string) (string, func(), error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return t.TempDir(), func() {}, nil
	}
	var specs []repoSpec
	for i := range 3 * maxParallelClones {
		specs = append(specs, repoSpec{URL: fmt.Sprintf("https://github.com/org/r%d", i)})
	}
	results := scanRepos(context.Background(), co, specs, mainScan, &progress{})
	if len(results) != len(specs) || peak > maxParallelClones || peak < 2 {
		t.Errorf("%d results, peak %d parallel clones, want at most %d", len(results), peak, maxParallelClones)
	}
}
