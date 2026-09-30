package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReadRepoRows(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string // rows joined with " | "
		edit  bool
	}{
		{"", "", false},
		{"repo=a", "a", false},
		{"repo=a&ref=v1&repo=b&ref=", "a v1 | b", false},
		{"repo=a&repo=b&ref=v1", "a v1 | b", false}, // refs pair by position
		{"repo=+a+&ref=+v1+&repo=&ref=x&repo=a&ref=v1&repo=a", "a v1 | a", false},
		{"repo=a&repo=b&repo=c&remove=1", "a | c", true},
		{"repo=a&remove=x", "a", true},
		{"repo=a&add=1", "a | ", true},
		{"repo=a&edit=1", "a", true},
	} {
		q, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		rows, edit := readRepoRows(q)
		var got []string
		for _, r := range rows {
			got = append(got, r.String())
		}
		if strings.Join(got, " | ") != tc.want || edit != tc.edit {
			t.Errorf("%q: rows = %q, edit = %v; want %q, %v", tc.query, strings.Join(got, " | "), edit, tc.want, tc.edit)
		}
	}

	// Add does nothing at the limit.
	q := url.Values{"add": {"1"}}
	for i := 0; i < maxRepos; i++ {
		q.Add("repo", fmt.Sprintf("r%d", i))
	}
	if rows, _ := readRepoRows(q); len(rows) != maxRepos {
		t.Errorf("add at the limit: %d rows, want %d", len(rows), maxRepos)
	}
}

func TestRepoQuery(t *testing.T) {
	if got := repoQuery([]repoSpec{{URL: "https://x/a"}, {URL: "git@x:b"}}); got != "repo=https%3A%2F%2Fx%2Fa&repo=git%40x%3Ab" {
		t.Errorf("without refs: %q", got)
	}
	if got := repoQuery([]repoSpec{{URL: "a", Ref: "v&1"}, {URL: "b"}, {}}); got != "repo=a&ref=v%261&repo=b&ref=" {
		t.Errorf("with refs: %q", got)
	}
}

func TestServeListsSkillsOfSeveralRepos(t *testing.T) {
	co := twoRepos(t)
	h := newServeHandler(co.checkout)
	code, body := get(t, h, "/?"+rowsQuery("https://github.com/org/b v1", "https://github.com/org/a"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		"3 skills found in 2 repositories",
		"<h2>deploy</h2>", "<h2>release</h2>", "<h2>review</h2>",
		"https://github.com/org/a · skills/deploy/SKILL.md",
		"https://github.com/org/b v1 · .claude/skills/release/SKILL.md",
		`value="https://github.com/org/a"`, `value="https://github.com/org/b"`, `value="v1"`,
		"<title>2 repositories · atlas</title>",
		// Recent search of both, and a link that prefills /cluster.
		`href="/?repo=https%3A%2F%2Fgithub.com%2Forg%2Fb&amp;ref=v1&amp;repo=https%3A%2F%2Fgithub.com%2Forg%2Fa&amp;ref=" aria-current="page"`,
		`href="/cluster?repo=https%3A%2F%2Fgithub.com%2Forg%2Fb&amp;ref=v1&amp;repo=https%3A%2F%2Fgithub.com%2Forg%2Fa&amp;ref=&amp;edit=1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	// One list sorted by name across repositories.
	d, rel, rev := strings.Index(body, "<h2>deploy</h2>"), strings.Index(body, "<h2>release</h2>"), strings.Index(body, "<h2>review</h2>")
	if d < 0 || d > rel || rel > rev {
		t.Error("skills of several repositories are not merged and sorted by name")
	}
	if strings.Count(body, `<span class="repo">`) != 2 || !strings.Contains(body, "3 skills</span>") {
		t.Errorf("history does not have one entry with both repositories:\n%s", body)
	}
}

func TestServeSeveralReposFilter(t *testing.T) {
	co := twoRepos(t)
	_, body := get(t, newServeHandler(co.checkout), "/?"+rowsQuery("https://github.com/org/a", "https://github.com/org/b")+"&filter=re")
	for _, want := range []string{
		"2 of 3 skills match &quot;re&quot;", "<h2>release</h2>", "<h2>review</h2>",
		`href="/?repo=https%3A%2F%2Fgithub.com%2Forg%2Fa&amp;repo=https%3A%2F%2Fgithub.com%2Forg%2Fb">Clear filter`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<h2>deploy</h2>") {
		t.Error("filter is not applied across repositories")
	}
}

func TestServeSeveralReposPartialFailure(t *testing.T) {
	co := twoRepos(t)
	co.errs = map[string]error{"https://github.com/org/b": errors.New("failed to clone https://github.com/org/b: boom")}
	h := newServeHandler(co.checkout)
	code, body := get(t, h, "/?"+rowsQuery("https://github.com/org/a", "https://github.com/org/b", "/etc"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		`class="message warnings"`, "Some repositories could not be scanned",
		"failed to clone https://github.com/org/b: boom", "invalid repository URL &#34;/etc&#34;",
		"<h2>deploy</h2>", "<h2>review</h2>", "2 skills found in 3 repositories",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	for _, c := range co.calls {
		if c == "/etc" {
			t.Error("checkout called for an invalid URL")
		}
	}
	if strings.Contains(body, "Recent searches") {
		t.Error("search with failed repositories is recorded in history")
	}
}

func TestServeSeveralReposAllFail(t *testing.T) {
	co := &repoCheckout{}
	code, body := get(t, newServeHandler(co.checkout), "/?"+rowsQuery("https://github.com/org/x", "https://github.com/org/y"))
	if code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
	if !strings.Contains(body, `class="message error"`) || !strings.Contains(body, "No repository could be scanned") ||
		strings.Count(body, "failed to clone") != 2 {
		t.Errorf("page does not list both failures as an error:\n%s", body)
	}
	if strings.Contains(body, "No skills found") {
		t.Error("\"No skills found\" shown when nothing was scanned")
	}
}

func TestServeRepoLimit(t *testing.T) {
	co := &repoCheckout{}
	var lines []string
	for i := 0; i <= maxRepos; i++ {
		lines = append(lines, fmt.Sprintf("https://github.com/org/r%d", i))
	}
	code, body := get(t, newServeHandler(co.checkout), "/?"+rowsQuery(lines...))
	if code != http.StatusBadRequest || !strings.Contains(body, "at most 10") {
		t.Errorf("status = %d, want 400 with a limit error:\n%s", code, body)
	}
	if len(co.calls) != 0 {
		t.Error("repositories cloned over the limit")
	}
}

// Without JavaScript the add/remove buttons submit the form; the server shows
// it edited and scans nothing.
func TestRepoFormEditWithoutJS(t *testing.T) {
	co, fc := twoRepos(t), &fakeClaude{}
	h := clusterHandler(co, fc)
	for _, page := range []string{"/", "/cluster"} {
		code, body := get(t, h, page+"?"+rowsQuery("https://github.com/org/a v1", "https://github.com/org/b")+"&add=1")
		if code != http.StatusOK || strings.Count(body, `<div class="repo-row">`) != 3 ||
			!strings.Contains(body, `value="v1"`) || !strings.Contains(body, `value="" placeholder="https://github.com/org/repo" aria-label="Repository URL" autofocus`) {
			t.Errorf("%s add: status = %d, want 3 rows with the new one focused:\n%s", page, code, body)
		}

		_, body = get(t, h, page+"?"+rowsQuery("https://github.com/org/a", "https://github.com/org/b")+"&remove=0")
		if strings.Count(body, `<div class="repo-row">`) != 1 || strings.Contains(body, `value="https://github.com/org/a"`) ||
			!strings.Contains(body, `name="remove" value="0"`) {
			t.Errorf("%s remove: want only repo b left:\n%s", page, body)
		}

		_, body = get(t, h, page+"?edit=1")
		if strings.Count(body, `<div class="repo-row">`) != 1 {
			t.Errorf("%s: empty form has no row to type into", page)
		}
	}
	if len(co.calls) != 0 || len(fc.prompts) != 0 {
		t.Errorf("editing the form scanned repositories: %q", co.calls)
	}
}

func TestRepoFormAddDisabledAtLimit(t *testing.T) {
	var lines []string
	for i := 0; i < maxRepos; i++ {
		lines = append(lines, fmt.Sprintf("https://github.com/org/r%d", i))
	}
	_, body := get(t, newServeHandler((&repoCheckout{}).checkout), "/?"+rowsQuery(lines...)+"&edit=1")
	if !strings.Contains(body, `name="add" value="1" class="add-repo" disabled`) {
		t.Errorf("add button is not disabled at %d repositories", maxRepos)
	}
}

func TestRepoListCarriesOverBetweenPages(t *testing.T) {
	co, fc := twoRepos(t), &fakeClaude{answer: `[{"name": "All", "skills": ["s1", "s2", "s3"]}]`}
	h := clusterHandler(co, fc)
	_, body := get(t, h, clusterQuery("https://github.com/org/a", "https://github.com/org/b v1"))
	link := `href="/?repo=https%3A%2F%2Fgithub.com%2Forg%2Fa&amp;ref=&amp;repo=https%3A%2F%2Fgithub.com%2Forg%2Fb&amp;ref=v1&amp;edit=1"`
	if !strings.Contains(body, link) {
		t.Fatalf("/cluster has no link %s:\n%s", link, body)
	}
	calls := len(co.calls)
	_, main := get(t, h, "/?repo=https%3A%2F%2Fgithub.com%2Forg%2Fa&ref=&repo=https%3A%2F%2Fgithub.com%2Forg%2Fb&ref=v1&edit=1")
	if !strings.Contains(main, `value="https://github.com/org/b"`) || !strings.Contains(main, `value="v1"`) || len(co.calls) != calls {
		t.Error("the link does not just prefill the main page")
	}
}

func TestClusterLegacyReposQuery(t *testing.T) {
	co := twoRepos(t)
	fc := &fakeClaude{answer: `[{"name": "All", "skills": ["s1", "s2", "s3"]}]`}
	code, body := get(t, clusterHandler(co, fc), legacyClusterQuery("https://github.com/org/a", "https://github.com/org/b v1"))
	if code != http.StatusOK || !strings.Contains(body, "3 skills in 1 cluster") || !strings.Contains(body, `value="v1"`) {
		t.Errorf("status = %d, want the old ?repos= links to work:\n%s", code, body)
	}
}

func TestServeReposScript(t *testing.T) {
	h := newServeHandler((&stubCheckout{}).checkout, withClaude((&fakeClaude{}).run, time.Minute))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/repos.js", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("status = %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, page := range []string{"/", "/cluster"} {
		if _, body := get(t, h, page); !strings.Contains(body, `<script src="/repos.js"></script>`) {
			t.Errorf("%s does not include the repository list script", page)
		}
	}
}
