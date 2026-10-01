package cli

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func postStar(t *testing.T, h http.Handler, form url.Values, js bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/star", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if js {
		req.Header.Set(starHeader, "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func starForm(repo, ref, path, star string) url.Values {
	return url.Values{"repo": {repo}, "ref": {ref}, "path": {path}, "star": {star}}
}

const bananaList = `<nav class="bananas chips"`

func TestServeStarSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, ".claude/skills/deploy", "---\nname: deploy\ndescription: Deploy\n---\n")
	writeSkill(t, root, "skills/review", "---\nname: code-review\n---\n")
	stub := &stubCheckout{dir: root}
	h := newServeHandler(stub.checkout)
	page := query("https://github.com/org/repo", "v1")

	_, body := get(t, h, page)
	if strings.Count(body, `aria-pressed="false"`) != 2 || strings.Contains(body, bananaList) {
		t.Fatalf("want two unstarred skills and no banana list:\n%s", body)
	}
	if !strings.Contains(body, `name="back" value="`+strings.ReplaceAll(page, "&", "&amp;")+`"`) {
		t.Errorf("star form does not return to %q", page)
	}

	form := starForm("https://github.com/org/repo", "v1", ".claude/skills/deploy/SKILL.md", "1")
	form.Set("back", page)
	rec := postStar(t, h, form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != page {
		t.Fatalf("status = %d, Location = %q, want 303 to %q", rec.Code, rec.Header().Get("Location"), page)
	}

	_, body = get(t, h, page)
	if strings.Count(body, `aria-pressed="true"`) != 1 || !strings.Contains(body, `name="star" value="0" class="banana" aria-pressed="true" aria-label="Banana for deploy"`) {
		t.Errorf("deploy is not starred:\n%s", body)
	}
	link := `<a href="/?repo=https%3A%2F%2Fgithub.com%2Forg%2Frepo&amp;ref=v1&amp;filter=deploy">`
	if !strings.Contains(body, bananaList) || !strings.Contains(body, link) {
		t.Errorf("banana list has no link %q:\n%s", link, body)
	}
	// The list is shown without a search too.
	if _, body := get(t, h, "/"); !strings.Contains(body, link) {
		t.Errorf("empty form has no banana list")
	}

	rec = postStar(t, h, starForm("https://github.com/org/repo", "v1", ".claude/skills/deploy/SKILL.md", "0"), false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Errorf("unstar: status = %d, Location = %q, want 303 to /", rec.Code, rec.Header().Get("Location"))
	}
	if _, body = get(t, h, page); strings.Contains(body, `aria-pressed="true"`) || strings.Contains(body, bananaList) {
		t.Errorf("banana was not taken back:\n%s", body)
	}
}

func TestServeStarFromScript(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/review", "---\nname: code-review\n---\n")
	stub := &stubCheckout{dir: root}
	h := newServeHandler(stub.checkout)

	rec := postStar(t, h, starForm("https://github.com/org/repo", "", "skills/review/SKILL.md", "1"), true)
	if rec.Code != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), bananaList) ||
		!strings.Contains(rec.Body.String(), `<span class="name">code-review</span>`) {
		t.Fatalf("status = %d, want 200 and the banana list:\n%s", rec.Code, rec.Body.String())
	}
	rec = postStar(t, h, starForm("https://github.com/org/repo", "", "skills/review/SKILL.md", "0"), true)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
		t.Errorf("status = %d, want 200 and an empty list:\n%s", rec.Code, rec.Body.String())
	}
}

func TestServeStarErrors(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/review", "---\nname: code-review\n---\n")
	for _, tc := range []struct {
		name   string
		err    error
		form   url.Values
		status int
	}{
		{"unknown skill", nil, starForm("https://github.com/org/repo", "", "../../etc/SKILL.md", "1"), http.StatusNotFound},
		{"local path", nil, starForm("/etc", "", "skills/review/SKILL.md", "1"), http.StatusBadRequest},
		{"clone failure", errors.New("failed to clone"), starForm("https://github.com/org/repo", "", "skills/review/SKILL.md", "1"), http.StatusBadGateway},
	} {
		stub := &stubCheckout{dir: root, err: tc.err}
		h := newServeHandler(stub.checkout)
		if rec := postStar(t, h, tc.form, false); rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.status)
		}
		if _, body := get(t, h, "/"); strings.Contains(body, bananaList) {
			t.Errorf("%s: a skill was starred", tc.name)
		}
	}

	stub := &stubCheckout{dir: root}
	if code, _ := get(t, newServeHandler(stub.checkout), "/star"); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /star: status = %d, want 405", code)
	}
}

func TestStarsLimit(t *testing.T) {
	var s stars
	for i := 0; i < maxStars+5; i++ {
		s.add(starEntry{starKey: starKey{Repo: repoSpec{URL: "https://example.com/r"}, Path: fmt.Sprintf("s%d/SKILL.md", i)}})
	}
	s.add(starEntry{starKey: starKey{Repo: repoSpec{URL: "https://example.com/r"}, Path: "s50/SKILL.md"}})
	got := s.list()
	if len(got) != maxStars || got[0].Path != "s50/SKILL.md" || got[1].Path != fmt.Sprintf("s%d/SKILL.md", maxStars+4) {
		t.Errorf("len = %d, first = %q, %q; want %d, newest first, no duplicates", len(got), got[0].Path, got[1].Path, maxStars)
	}
}

func TestLocalPath(t *testing.T) {
	for back, want := range map[string]string{
		"/?repo=x&filter=y":    "/?repo=x&filter=y",
		"":                     "/",
		"https://evil.example": "/",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
	} {
		if got := localPath(back); got != want {
			t.Errorf("localPath(%q) = %q, want %q", back, got, want)
		}
	}
}
