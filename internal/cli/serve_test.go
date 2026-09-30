package cli

import (
	"context"
	"errors"
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
