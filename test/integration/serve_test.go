//go:build integration

package integration

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startServe runs `atlas serve` on a free port and returns its base URL.
func startServe(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, atlasBin, "serve", "--addr", "127.0.0.1:0")
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
		return strings.TrimSpace(strings.TrimPrefix(line, prefix))
	case <-time.After(10 * time.Second):
		t.Fatal("atlas serve did not start")
		return ""
	}
}

func fetch(t *testing.T, base, repo string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(base + "/?" + url.Values{"repo": {repo}}.Encode())
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

	code, body = fetch(t, base, "https://github.com/simpletonDL/no-such-repo-atlas")
	if code != http.StatusBadGateway || !strings.Contains(body, "failed to clone") {
		t.Errorf("missing repo: status = %d, want 502 with clone error", code)
	}
}
