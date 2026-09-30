package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// repoCheckout serves a directory (or an error) per repository URL.
type repoCheckout struct {
	mu    sync.Mutex
	dirs  map[string]string
	errs  map[string]error
	calls []string
}

func (c *repoCheckout) checkout(_ context.Context, source, ref string) (string, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, strings.TrimSpace(source+" "+ref))
	if err := c.errs[source]; err != nil {
		return "", func() {}, err
	}
	dir, ok := c.dirs[source]
	if !ok {
		return "", func() {}, fmt.Errorf("failed to clone %s: not found", source)
	}
	return dir, func() {}, nil
}

// fakeClaude returns a canned answer and records the prompts it got.
type fakeClaude struct {
	answer  string
	err     error
	prompts []string
}

func (f *fakeClaude) run(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	return f.answer, f.err
}

func clusterQuery(lines ...string) string {
	return "/cluster?" + url.Values{"repos": {strings.Join(lines, "\n")}}.Encode()
}

// twoRepos has repo a with skills deploy (s1) and review (s2) and repo b with
// skill release (s3).
func twoRepos(t *testing.T) *repoCheckout {
	t.Helper()
	a, b := t.TempDir(), t.TempDir()
	writeSkill(t, a, "skills/deploy", "---\nname: deploy\ndescription: Deploy the service\n---\nSECRET BODY\n")
	writeSkill(t, a, "skills/review", "---\nname: review\ndescription: Review <b>diffs</b>\n---\n")
	writeSkill(t, b, ".claude/skills/release", "---\nname: release\ndescription: Cut a release\n---\n")
	return &repoCheckout{dirs: map[string]string{
		"https://github.com/org/a": a,
		"https://github.com/org/b": b,
	}}
}

func clusterHandler(co *repoCheckout, fc *fakeClaude) http.Handler {
	return newServeHandler(co.checkout, withClaude(fc.run, time.Minute))
}

func TestClusterForm(t *testing.T) {
	co, fc := &repoCheckout{}, &fakeClaude{}
	code, body := get(t, clusterHandler(co, fc), "/cluster")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if !strings.Contains(body, `<form method="get" action="/cluster">`) || !strings.Contains(body, `<textarea name="repos"`) {
		t.Errorf("page has no repos form:\n%s", body)
	}
	if !strings.Contains(body, `href="/"`) {
		t.Error("page has no link back to the main page")
	}
	if len(co.calls) != 0 || len(fc.prompts) != 0 {
		t.Error("checkout or claude called without repos")
	}
	if _, main := get(t, clusterHandler(co, fc), "/"); !strings.Contains(main, `href="/cluster"`) {
		t.Error("main page has no link to /cluster")
	}
}

func TestClusterHappyPath(t *testing.T) {
	co := twoRepos(t)
	fc := &fakeClaude{answer: "```json\n" + `[
		{"name": "Shipping", "description": "Getting code to production", "skills": ["s1", "s3"]},
		{"name": "Quality", "description": "Code review", "skills": ["s2"]}
	]` + "\n```"}

	code, body := get(t, clusterHandler(co, fc), clusterQuery("https://github.com/org/a", "  https://github.com/org/b v1  ", ""))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	sort.Strings(co.calls) // repositories are cloned in parallel
	if len(co.calls) != 2 || co.calls[1] != "https://github.com/org/b v1" {
		t.Errorf("checkout calls = %q", co.calls)
	}
	for _, want := range []string{
		"3 skills in 2 clusters",
		"<h2>Shipping", "Getting code to production", "<h2>Quality",
		"<h3>deploy</h3>", "Deploy the service", "https://github.com/org/a · skills/deploy/SKILL.md",
		"https://github.com/org/b v1 · .claude/skills/release/SKILL.md",
		"Review &lt;b&gt;diffs&lt;/b&gt;", // escaped
		"https://github.com/org/b v1</textarea>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if strings.Contains(body, "<h2>Other") {
		t.Error("Other cluster shown although every skill is clustered")
	}
	if a, b := strings.Index(body, "<h3>deploy</h3>"), strings.Index(body, "<h3>release</h3>"); a < 0 || b < a ||
		strings.Index(body, "<h2>Quality") < b {
		t.Error("clusters or their skills are out of order")
	}

	if len(fc.prompts) != 1 {
		t.Fatalf("claude called %d times", len(fc.prompts))
	}
	prompt := fc.prompts[0]
	for _, want := range []string{`"id": "s1"`, `"name": "deploy"`, `"description": "Deploy the service"`, `"id": "s3"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt has no %q:\n%s", want, prompt)
		}
	}
	for _, leak := range []string{"SECRET BODY", "SKILL.md", "github.com"} {
		if strings.Contains(prompt, leak) {
			t.Errorf("prompt leaks %q", leak)
		}
	}
}

func TestClusterOmittedSkillsGoToOther(t *testing.T) {
	co := twoRepos(t)
	fc := &fakeClaude{answer: `[{"name": "Shipping", "description": "", "skills": ["s1"]}]`}
	_, body := get(t, clusterHandler(co, fc), clusterQuery("https://github.com/org/a", "https://github.com/org/b"))

	other := strings.Index(body, "<h2>"+otherClusterName)
	if other < 0 {
		t.Fatalf("page has no Other cluster:\n%s", body)
	}
	if strings.Index(body, "<h3>review</h3>") < other || strings.Index(body, "<h3>release</h3>") < other {
		t.Error("omitted skills are not in the Other cluster")
	}
	if strings.Count(body, "<h3>") != 3 {
		t.Error("some skill is shown more than once or missing")
	}
}

func TestClusterIgnoresUnknownAndDuplicateIDs(t *testing.T) {
	all := []clusterSkill{{ID: "s1"}, {ID: "s2"}, {ID: "s3"}}
	all[0].Name, all[1].Name, all[2].Name = "a", "b", "c"
	got, err := parseClusters(`Here you go:
[
  {"name": "One", "skills": ["s1", "s99", "s1", 2, "s2"]},
  {"name": "Ghosts", "skills": ["x", "s42"]},
  {"name": "", "skills": ["s2", "s3"]}
]`, all)
	if err != nil {
		t.Fatal(err)
	}
	var summary []string
	for _, c := range got {
		var ids []string
		for _, s := range c.Skills {
			ids = append(ids, s.ID)
		}
		summary = append(summary, c.Name+"="+strings.Join(ids, ","))
	}
	if want := "One=s1,s2 | Unnamed cluster=s3"; strings.Join(summary, " | ") != want {
		t.Errorf("clusters = %q, want %q", strings.Join(summary, " | "), want)
	}
}

func TestClusterInvalidJSON(t *testing.T) {
	for _, answer := range []string{"I cannot do that", `[{"name": "x", "skills": ["s1"]`, `{"name": "x"}`} {
		co := twoRepos(t)
		fc := &fakeClaude{answer: answer}
		code, body := get(t, clusterHandler(co, fc), clusterQuery("https://github.com/org/a"))
		if code != http.StatusBadGateway || !strings.Contains(body, "claude returned invalid JSON") {
			t.Errorf("%q: status = %d, want 502 with an invalid JSON error:\n%s", answer, code, body)
		}
		if strings.Contains(body, `class="cluster"`) {
			t.Errorf("%q: clusters are shown", answer)
		}
	}
}

func TestClusterClaudeError(t *testing.T) {
	co := twoRepos(t)
	fc := &fakeClaude{err: fmt.Errorf("%w (%q): install Claude Code or pass --claude-bin", errClaudeNotFound, "claude")}
	code, body := get(t, clusterHandler(co, fc), clusterQuery("https://github.com/org/a"))
	if code != http.StatusBadGateway || !strings.Contains(body, "claude CLI not found") {
		t.Errorf("status = %d, want 502 with a clear message:\n%s", code, body)
	}
	if !strings.Contains(body, "2 skills") {
		t.Error("scanned repositories are not shown")
	}
}

func TestClusterPerRepoErrors(t *testing.T) {
	co := twoRepos(t)
	co.errs = map[string]error{"https://github.com/org/b": errors.New("failed to clone https://github.com/org/b: boom")}
	fc := &fakeClaude{answer: `[{"name": "All", "skills": ["s1", "s2"]}]`}

	code, body := get(t, clusterHandler(co, fc), clusterQuery(
		"https://github.com/org/a", "https://github.com/org/b", "/etc", "https://github.com/org/c main extra"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		"Some repositories could not be scanned",
		"failed to clone https://github.com/org/b: boom",
		"invalid repository URL &#34;/etc&#34;",
		"invalid line",
		"<h2>All", "<h3>deploy</h3>", "<h3>review</h3>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	for _, c := range co.calls {
		if c == "/etc" || strings.Contains(c, "org/c") {
			t.Errorf("checkout called for invalid line %q", c)
		}
	}
}

func TestClusterValidation(t *testing.T) {
	co, fc := &repoCheckout{}, &fakeClaude{}
	code, body := get(t, clusterHandler(co, fc), clusterQuery("/etc", "file:///etc", "-uhelp"))
	if code != http.StatusBadRequest || !strings.Contains(body, "no valid repositories") {
		t.Errorf("status = %d, want 400:\n%s", code, body)
	}
	if strings.Count(body, "invalid repository URL") != 3 {
		t.Error("not every invalid URL is reported")
	}
	if len(co.calls) != 0 || len(fc.prompts) != 0 {
		t.Error("checkout or claude called for invalid URLs")
	}
}

func TestClusterRepoLimitAndDedupe(t *testing.T) {
	co, fc := &repoCheckout{dirs: map[string]string{}}, &fakeClaude{}
	var lines []string
	for i := 0; i <= maxClusterRepos; i++ {
		lines = append(lines, fmt.Sprintf("https://github.com/org/r%d", i))
	}
	code, body := get(t, clusterHandler(co, fc), clusterQuery(lines...))
	if code != http.StatusBadRequest || !strings.Contains(body, "at most 10") {
		t.Errorf("status = %d, want 400 with a limit error:\n%s", code, body)
	}
	if len(co.calls) != 0 {
		t.Error("repositories cloned over the limit")
	}

	// Duplicates don't count against the limit and are cloned once.
	dir := t.TempDir()
	for i := 0; i < maxClusterRepos; i++ {
		co.dirs[lines[i]] = dir
	}
	dup := append(append([]string{}, lines[:maxClusterRepos]...), lines[0], lines[1])
	if code, body := get(t, clusterHandler(co, fc), clusterQuery(dup...)); code != http.StatusOK || !strings.Contains(body, "No skills found") {
		t.Errorf("status = %d, want 200 with no skills:\n%s", code, body)
	}
	if len(co.calls) != maxClusterRepos {
		t.Errorf("checkout called %d times, want %d", len(co.calls), maxClusterRepos)
	}
	if len(fc.prompts) != 0 {
		t.Error("claude called without skills")
	}
}

func TestClusterMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	clusterHandler(&repoCheckout{}, &fakeClaude{}).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/cluster", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /cluster: status = %d, want 405", rec.Code)
	}
}

func TestParseClaudeEnvelope(t *testing.T) {
	got, err := parseClaudeEnvelope([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"[1]"}`))
	if err != nil || got != "[1]" {
		t.Errorf("envelope: got %q, %v", got, err)
	}
	if _, err := parseClaudeEnvelope([]byte(`{"type":"result","is_error":true,"result":"Invalid API key"}`)); err == nil ||
		!strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error envelope: err = %v", err)
	}
	if got, _ := parseClaudeEnvelope([]byte("[]\n")); got != "[]\n" {
		t.Errorf("raw output: got %q", got)
	}
}

func TestClaudeCLI(t *testing.T) {
	if _, err := claudeCLI(filepath.Join(t.TempDir(), "no-claude"))(context.Background(), "hi"); !errors.Is(err, errClaudeNotFound) {
		t.Errorf("missing binary: err = %v", err)
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\necho \"$@\" > \"$(dirname \"$0\")/args\"\ncat > \"$(dirname \"$0\")/stdin\"\n" +
		`echo '{"type":"result","is_error":false,"result":"ok"}'` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := claudeCLI(bin)(context.Background(), "the prompt")
	if err != nil || got != "ok" {
		t.Fatalf("got %q, %v", got, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if strings.TrimSpace(string(args)) != "-p --output-format json" || string(stdin) != "the prompt" {
		t.Errorf("args = %q, stdin = %q", args, stdin)
	}

	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'boom' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := claudeCLI(bin)(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "claude failed: boom") {
		t.Errorf("failing binary: err = %v", err)
	}
}
