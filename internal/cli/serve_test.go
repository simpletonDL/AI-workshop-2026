package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// stubCheckout serves dir for any source and records what it was asked for.
type stubCheckout struct {
	dir         string
	err         error
	source, ref string
	calls       int
}

func (s *stubCheckout) checkout(_ context.Context, source, ref string) (string, func(), error) {
	s.calls++
	s.source, s.ref = source, ref
	if s.err != nil {
		return "", func() {}, s.err
	}
	return s.dir, func() {}, nil
}

func get(t *testing.T, h http.Handler, target string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec.Code, rec.Body.String()
}

func query(repo, ref string) string {
	v := url.Values{"repo": {repo}}
	if ref != "" {
		v.Set("ref", ref)
	}
	return "/?" + v.Encode()
}

func TestServeForm(t *testing.T) {
	stub := &stubCheckout{}
	code, body := get(t, newServeHandler(stub.checkout), "/")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, `<form`) || !strings.Contains(body, `name="repo"`) || !strings.Contains(body, `name="ref"`) {
		t.Errorf("page has no repo/ref form:\n%s", body)
	}
	if stub.calls != 0 {
		t.Errorf("checkout called %d times without a repo", stub.calls)
	}
}

func TestServeListsSkills(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, ".claude/skills/deploy", "---\nname: deploy\ndescription: Deploy the service to staging\n---\n")
	writeSkill(t, root, "skills/review", "---\nname: code-review\ndescription: Review <b>diffs</b>\n---\n")
	stub := &stubCheckout{dir: root}

	code, body := get(t, newServeHandler(stub.checkout), query("https://github.com/org/repo", "v1"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	if stub.source != "https://github.com/org/repo" || stub.ref != "v1" {
		t.Errorf("checkout(%q, %q), want repo URL and v1", stub.source, stub.ref)
	}
	for _, want := range []string{
		"2 skills found",
		"deploy", "Deploy the service to staging", ".claude/skills/deploy/SKILL.md",
		"code-review", "skills/review/SKILL.md",
		"Review &lt;b&gt;diffs&lt;/b&gt;",                   // escaped
		`value="https://github.com/org/repo"`, `value="v1"`, // form keeps input
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if strings.Index(body, "code-review") > strings.Index(body, "Deploy the service") {
		t.Error("skills are not sorted by name")
	}
}

func TestServeNoSkills(t *testing.T) {
	stub := &stubCheckout{dir: t.TempDir()}
	code, body := get(t, newServeHandler(stub.checkout), query("https://github.com/org/empty", ""))
	if code != http.StatusOK || !strings.Contains(body, "No skills found") {
		t.Errorf("status = %d, want 200 and \"No skills found\":\n%s", code, body)
	}
}

func TestServeCloneFailure(t *testing.T) {
	stub := &stubCheckout{err: errors.New("failed to clone https://github.com/org/nope: not found")}
	code, body := get(t, newServeHandler(stub.checkout), query("https://github.com/org/nope", ""))
	if code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", code, http.StatusBadGateway)
	}
	if !strings.Contains(body, "failed to clone") {
		t.Errorf("page has no clone error:\n%s", body)
	}
}

func TestServeRejectsNonRemoteSources(t *testing.T) {
	for _, repo := range []string{"/etc", ".", "file:///etc", "ext::sh -c id", "-uhelp", "https://"} {
		stub := &stubCheckout{dir: t.TempDir()}
		code, body := get(t, newServeHandler(stub.checkout), query(repo, ""))
		if code != http.StatusBadRequest || !strings.Contains(body, "invalid repository URL") {
			t.Errorf("%q: status = %d, want 400 with an error message", repo, code)
		}
		if stub.calls != 0 {
			t.Errorf("%q: checkout was called", repo)
		}
	}
}

func TestValidateRemoteAcceptsURLs(t *testing.T) {
	for _, repo := range []string{
		"https://github.com/org/repo",
		"HTTPS://github.com/org/repo.git",
		"http://example.com/repo.git",
		"ssh://git@github.com/org/repo.git",
		"git://example.com/repo.git",
		"git@github.com:org/repo.git",
	} {
		if err := validateRemote(repo); err != nil {
			t.Errorf("%q: %v", repo, err)
		}
	}
}

func TestServeUnknownPathAndMethod(t *testing.T) {
	h := newServeHandler((&stubCheckout{}).checkout)
	if code, _ := get(t, h, "/nope"); code != http.StatusNotFound {
		t.Errorf("GET /nope: status = %d, want 404", code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: status = %d, want 405", rec.Code)
	}
}

func TestServeShowsSkillText(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/deploy", "---\nname: deploy\n---\n# Deploy\n\nRun <script>alert(1)</script> & wait.\n")
	stub := &stubCheckout{dir: root}

	_, body := get(t, newServeHandler(stub.checkout), query("https://github.com/org/repo", ""))
	for _, want := range []string{
		`<details class="skill">`,
		"---\nname: deploy\n---\n# Deploy", // frontmatter included
		"Run &lt;script&gt;alert(1)&lt;/script&gt; &amp; wait.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "text truncated") {
		t.Error("skill text is not escaped or is marked as truncated")
	}
}

func TestServeTruncatesLargeSkillText(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/big", "---\nname: big\n---\n"+strings.Repeat("x", maxSkillText)+"TAIL")
	stub := &stubCheckout{dir: root}

	_, body := get(t, newServeHandler(stub.checkout), query("https://github.com/org/repo", ""))
	if !strings.Contains(body, "text truncated") {
		t.Error("large skill is not marked as truncated")
	}
	if strings.Contains(body, "TAIL") {
		t.Error("text past the limit is shown")
	}
}

func TestServeRecentSearches(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/deploy", "---\nname: deploy\n---\n")
	stub := &stubCheckout{dir: root}
	h := newServeHandler(stub.checkout)

	if _, body := get(t, h, "/"); strings.Contains(body, "Recent searches") {
		t.Error("history is shown before any search")
	}

	get(t, h, query("https://github.com/org/a", ""))
	get(t, h, query("https://github.com/org/b", "v1"))
	get(t, h, query("/etc", "")) // rejected: not recorded
	stub.err = errors.New("failed to clone")
	get(t, h, query("https://github.com/org/broken", "")) // clone error: not recorded
	stub.err = nil
	_, body := get(t, h, query("https://github.com/org/a", "")) // repeat moves to top

	if strings.Contains(body, "/etc") || strings.Contains(body, "org/broken") {
		t.Error("failed searches are recorded")
	}
	linkA := `href="/?repo=https%3a%2f%2fgithub.com%2forg%2fa"`
	linkB := `href="/?repo=https%3a%2f%2fgithub.com%2forg%2fb&amp;ref=v1"`
	a, b := strings.Index(body, linkA), strings.Index(body, linkB)
	if a < 0 || b < 0 {
		t.Fatalf("history has no links %q and %q:\n%s", linkA, linkB, body)
	}
	if a > b {
		t.Error("repeated search is not moved to the top")
	}
	if strings.Count(body, linkA) != 1 {
		t.Error("repeated search is duplicated")
	}
	if !strings.Contains(body, "1 skill</span>") || !strings.Contains(body, `aria-current="page"`) {
		t.Error("history has no skill count or no current entry")
	}
}

func TestHistoryLimit(t *testing.T) {
	var h history
	for i := 0; i < maxHistory+5; i++ {
		h.add(historyEntry{Repo: fmt.Sprintf("https://example.com/r%d", i)})
	}
	got := h.list()
	if len(got) != maxHistory {
		t.Fatalf("len = %d, want %d", len(got), maxHistory)
	}
	if want := fmt.Sprintf("https://example.com/r%d", maxHistory+4); got[0].Repo != want {
		t.Errorf("newest = %q, want %q", got[0].Repo, want)
	}
}

func filterQuery(repo, ref, filter string) string {
	v := url.Values{"repo": {repo}, "filter": {filter}}
	if ref != "" {
		v.Set("ref", ref)
	}
	return "/?" + v.Encode()
}

// filterRepo is a repository with three skills; only the name of "code-review"
// contains "review", while "deploy" mentions it in its description and path.
func filterRepo(t *testing.T) *stubCheckout {
	t.Helper()
	root := t.TempDir()
	writeSkill(t, root, "skills/review-tools", "---\nname: deploy\ndescription: Deploy after review\n---\n")
	writeSkill(t, root, "skills/cr", "---\nname: Code-Review\ndescription: Review diffs\n---\n")
	writeSkill(t, root, "skills/lint", "---\nname: lint\ndescription: Lint code\n---\n")
	return &stubCheckout{dir: root}
}

func TestServeFilterByName(t *testing.T) {
	for _, filter := range []string{"review", "REVIEW", "  ReV  ", "de-re"} {
		stub := filterRepo(t)
		code, body := get(t, newServeHandler(stub.checkout), filterQuery("https://github.com/org/repo", "v1", filter))
		if code != http.StatusOK {
			t.Fatalf("%q: status = %d", filter, code)
		}
		trimmed := strings.TrimSpace(filter)
		for _, want := range []string{
			"<h2>Code-Review</h2>",
			"1 of 3 skills matches &quot;" + trimmed + "&quot;",
			`value="` + trimmed + `"`, // form keeps the filter
			`href="/?repo=https%3a%2f%2fgithub.com%2forg%2frepo&amp;ref=v1">Clear filter`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%q: page has no %q:\n%s", filter, want, body)
			}
		}
		for _, unwanted := range []string{"<h2>deploy</h2>", "<h2>lint</h2>"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("%q: page shows filtered-out skill %q", filter, unwanted)
			}
		}
	}
}

func TestServeFilterSubstringCount(t *testing.T) {
	stub := filterRepo(t)
	_, body := get(t, newServeHandler(stub.checkout), filterQuery("https://github.com/org/repo", "", "e"))
	for _, want := range []string{"<h2>Code-Review</h2>", "<h2>deploy</h2>", "2 of 3 skills match &quot;e&quot;"} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if strings.Contains(body, "<h2>lint</h2>") {
		t.Error("lint does not match \"e\" but is shown")
	}
}

func TestServeFilterNoMatches(t *testing.T) {
	stub := filterRepo(t)
	code, body := get(t, newServeHandler(stub.checkout), filterQuery("https://github.com/org/repo", "", "xyz"))
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, "No skills match &quot;xyz&quot;") || !strings.Contains(body, "Clear filter") {
		t.Errorf("page has no \"no matches\" message with a clear link:\n%s", body)
	}
	if strings.Contains(body, "No skills found") || strings.Contains(body, `<details class="skill">`) {
		t.Error("page shows \"No skills found\" or skills when nothing matches")
	}
}

func TestServeFilterEmptyRepo(t *testing.T) {
	stub := &stubCheckout{dir: t.TempDir()}
	_, body := get(t, newServeHandler(stub.checkout), filterQuery("https://github.com/org/empty", "", "xyz"))
	if !strings.Contains(body, "No skills found") || strings.Contains(body, "No skills match") {
		t.Errorf("repo without skills: want \"No skills found\":\n%s", body)
	}
}

func TestServeEmptyFilter(t *testing.T) {
	for _, filter := range []string{"", "   "} {
		stub := filterRepo(t)
		_, body := get(t, newServeHandler(stub.checkout), filterQuery("https://github.com/org/repo", "", filter))
		if !strings.Contains(body, "3 skills found") || strings.Contains(body, "Clear filter") {
			t.Errorf("%q: filter is applied:\n%s", filter, body)
		}
		for _, name := range []string{"Code-Review", "deploy", "lint"} {
			if !strings.Contains(body, "<h2>"+name+"</h2>") {
				t.Errorf("%q: page has no skill %q", filter, name)
			}
		}
	}
}

func TestServeFilterDoesNotAffectHistory(t *testing.T) {
	stub := filterRepo(t)
	h := newServeHandler(stub.checkout)
	get(t, h, filterQuery("https://github.com/org/repo", "", "lint"))
	_, body := get(t, h, filterQuery("https://github.com/org/repo", "", "xyz"))

	link := `href="/?repo=https%3a%2f%2fgithub.com%2forg%2frepo"`
	if strings.Count(body, link) != 2 { // history entry + clear filter link
		t.Errorf("want one history entry and a clear link %q:\n%s", link, body)
	}
	if strings.Contains(body, "filter=") {
		t.Error("history links contain the filter")
	}
	if !strings.Contains(body, "3 skills</span>") {
		t.Error("history does not record the unfiltered skill count")
	}
	if !strings.Contains(body, `aria-current="page"`) {
		t.Error("filtered search is not marked as current in history")
	}
}

func TestServeFilterEscaped(t *testing.T) {
	stub := filterRepo(t)
	filter := `"><script>alert(1)</script>`
	_, body := get(t, newServeHandler(stub.checkout), filterQuery("https://github.com/org/repo", "", filter))
	if strings.Contains(body, "<script>") {
		t.Errorf("filter is not escaped:\n%s", body)
	}
	if !strings.Contains(body, "No skills match &quot;&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;&quot;") {
		t.Errorf("page has no escaped filter:\n%s", body)
	}
}

func TestFilterSkills(t *testing.T) {
	all := []skillView{{}, {}, {}}
	all[0].Name, all[1].Name, all[2].Name = "Deploy", "code-review", "lint"
	if got := filterSkills(all, ""); len(got) != 3 {
		t.Errorf("empty filter: len = %d, want 3", len(got))
	}
	if got := filterSkills(all, "DEP"); len(got) != 1 || got[0].Name != "Deploy" {
		t.Errorf("DEP: got %v", got)
	}
	if got := filterSkills(all, "nope"); len(got) != 0 {
		t.Errorf("nope: got %v", got)
	}
}
