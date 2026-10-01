//go:build integration

package integration

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// startServe runs `atlas serve` on a free port with extra flags and returns
// its base URL.
func startServe(t *testing.T, flags ...string) string {
	t.Helper()
	base, _ := startServeCmd(t, nil, flags...)
	return base
}

// startServeCmd is startServe that sends the server's logs (stderr) to
// stderr and also returns the running command.
func startServeCmd(t *testing.T, stderr io.Writer, flags ...string) (string, *exec.Cmd) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, atlasBin, append([]string{"serve", "--addr", "127.0.0.1:0"}, flags...)...)
	cmd.Stderr = stderr
	// Interrupt instead of kill, so the server shuts down cleanly.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- line
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case line := <-lines:
		const prefix = "Serving atlas on "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("unexpected serve output %q", line)
		}
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), cmd
	case <-time.After(10 * time.Second):
		t.Fatal("atlas serve did not start")
		return "", nil
	}
}

func fetch(t *testing.T, base, repo string) (int, string) {
	t.Helper()
	return fetchQuery(t, base, url.Values{"repo": {repo}})
}

func fetchQuery(t *testing.T, base string, q url.Values) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(base + "/?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestServeListsSkills(t *testing.T) {
	base := startServe(t)

	code, body := fetch(t, base, repoClaudeOnly)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, name := range []string{"commit", "dev", "po", "pr", "review"} {
		if !strings.Contains(body, "<h2>"+name+"</h2>") {
			t.Errorf("page has no skill %q", name)
		}
	}
	if !strings.Contains(body, ".claude/skills/commit/SKILL.md") {
		t.Error("page has no skill path")
	}
	if !strings.Contains(body, `<details class="skill">`) || !strings.Contains(body, "<pre>---\n") {
		t.Error("page has no expandable SKILL.md text")
	}

	code, body = fetch(t, base, "https://github.com/simpletonDL/no-such-repo-atlas")
	if code != http.StatusBadGateway || !strings.Contains(body, "failed to clone") {
		t.Errorf("missing repo: status = %d, want 502 with clone error", code)
	}
	if !strings.Contains(body, "Recent searches") || strings.Count(body, `<span class="repo">`) != 1 {
		t.Error("history does not show exactly the one successful search")
	}
}

func TestServeListsSkillsOfSeveralRepos(t *testing.T) {
	base := startServe(t)

	code, body := fetchQuery(t, base, url.Values{"repo": {repoClaudeOnly, repoMultiAgent, "https://github.com/simpletonDL/no-such-repo-atlas"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		"<h2>commit</h2>", "<h2>fh</h2>", "<h2>review</h2>",
		repoClaudeOnly + " · .claude/skills/commit/SKILL.md",
		repoMultiAgent + " · .cursor/skills/fh/SKILL.md",
		"Some repositories could not be scanned", "failed to clone",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	// One list sorted by name across repositories: fh sits between dev and po.
	if dev, fh, po := strings.Index(body, "<h2>dev</h2>"), strings.Index(body, "<h2>fh</h2>"), strings.Index(body, "<h2>po</h2>"); dev > fh || fh > po {
		t.Error("skills of several repositories are not merged and sorted by name")
	}
}

func TestServeFiltersSkillsByName(t *testing.T) {
	base := startServe(t)

	code, body := fetchQuery(t, base, url.Values{"repo": {repoClaudeOnly}, "filter": {"REVIEW"}})
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	if !strings.Contains(body, "<h2>review</h2>") || strings.Contains(body, "<h2>commit</h2>") {
		t.Error("filter does not keep only matching skills")
	}
	if !strings.Contains(body, "Clear filter") {
		t.Error("page has no clear filter link")
	}

	_, body = fetchQuery(t, base, url.Values{"repo": {repoClaudeOnly}, "filter": {"no-such-skill"}})
	if !strings.Contains(body, "No skills match &quot;no-such-skill&quot;") {
		t.Error("page has no \"no matches\" message")
	}
	if strings.Count(body, `<span class="repo">`) != 1 {
		t.Error("filtered searches are recorded as separate history entries")
	}
}

func TestServeReportsProgress(t *testing.T) {
	base := startServe(t)
	const id = "integration-progress-0001"
	client := &http.Client{Timeout: 2 * time.Minute}

	req, err := http.NewRequest(http.MethodGet, base+"/?"+url.Values{"repo": {repoClaudeOnly}}.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Atlas-Progress", id)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<h2>review</h2>") {
		t.Fatalf("status = %d, body:\n%s", resp.StatusCode, body)
	}

	resp, err = client.Get(base + "/progress?id=" + id)
	if err != nil {
		t.Fatal(err)
	}
	progress, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(progress), `"percent":100`) || !strings.Contains(string(progress), `"done":true`) {
		t.Errorf("progress after the page: status = %d, body %s", resp.StatusCode, progress)
	}

	resp, err = client.Get(base + "/progress.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/progress.js status = %d", resp.StatusCode)
	}
}

// syncBuffer is a string buffer safe for a concurrent writer and reader.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServeReusesClonedRepository(t *testing.T) {
	cacheDir := t.TempDir()
	var logs syncBuffer
	base, cmd := startServeCmd(t, &logs, "--cache-dir", cacheDir)

	for i := 0; i < 2; i++ {
		if code, body := fetch(t, base, repoClaudeOnly); code != http.StatusOK || !strings.Contains(body, "<h2>review</h2>") {
			t.Fatalf("request %d: status = %d, body:\n%s", i, code, body)
		}
	}
	if n := strings.Count(logs.String(), "repo cache miss"); n != 1 {
		t.Errorf("repository cloned %d times, want 1; logs:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "repo cache hit") {
		t.Errorf("second request is not a cache hit; logs:\n%s", logs.String())
	}
	if entries, _ := os.ReadDir(cacheDir); len(entries) != 1 {
		t.Errorf("cache dir has %d entries, want 1 per-process dir", len(entries))
	}

	// On shutdown the server removes its checkouts.
	_ = cmd.Process.Signal(os.Interrupt)
	_ = cmd.Wait()
	if entries, _ := os.ReadDir(cacheDir); len(entries) != 0 {
		t.Errorf("cache dir not cleaned up on shutdown: %v", entries)
	}
}

func TestServeCacheDisabled(t *testing.T) {
	var logs syncBuffer
	base, _ := startServeCmd(t, &logs, "--cache-ttl", "0")
	for i := 0; i < 2; i++ {
		if code, _ := fetch(t, base, repoClaudeOnly); code != http.StatusOK {
			t.Fatalf("request %d: status = %d", i, code)
		}
	}
	if strings.Contains(logs.String(), "repo cache") {
		t.Errorf("cache used with --cache-ttl 0; logs:\n%s", logs.String())
	}
}

func TestServeStarsSkills(t *testing.T) {
	base := startServe(t)
	// No redirects: the 303 back to the page is checked as is.
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	star := func(path, value string) int {
		t.Helper()
		resp, err := client.PostForm(base+"/star", url.Values{"repo": {repoClaudeOnly}, "path": {path}, "star": {value}, "back": {"/?repo=x"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusSeeOther && resp.Header.Get("Location") != "/?repo=x" {
			t.Errorf("Location = %q, want /?repo=x", resp.Header.Get("Location"))
		}
		return resp.StatusCode
	}

	if code := star(".claude/skills/review/SKILL.md", "1"); code != http.StatusSeeOther {
		t.Fatalf("star: status = %d, want 303", code)
	}
	if code := star(".claude/skills/no-such-skill/SKILL.md", "1"); code != http.StatusNotFound {
		t.Errorf("unknown skill: status = %d, want 404", code)
	}
	_, body := fetch(t, base, repoClaudeOnly)
	if !strings.Contains(body, `<nav class="bananas chips"`) || !strings.Contains(body, `<span class="name">review</span>`) ||
		strings.Count(body, `aria-pressed="true"`) != 1 {
		t.Errorf("review is not starred:\n%s", body)
	}

	if code := star(".claude/skills/review/SKILL.md", "0"); code != http.StatusSeeOther {
		t.Fatalf("unstar: status = %d, want 303", code)
	}
	if _, body = fetch(t, base, repoClaudeOnly); strings.Contains(body, `aria-pressed="true"`) {
		t.Error("banana was not taken back")
	}
}
