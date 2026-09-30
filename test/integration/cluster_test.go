//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude writes a script that stands in for `claude -p`: it saves its
// arguments and stdin next to itself and prints a canned JSON envelope, so
// the test needs neither Claude nor an API key.
func fakeClaude(t *testing.T, result string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	envelope := `{"type":"result","subtype":"success","is_error":false,"result":` + jsonString(result) + `}`
	script := "#!/bin/sh\n" +
		"d=$(dirname \"$0\")\n" +
		"echo \"$@\" > \"$d/args\"\n" +
		"cat > \"$d/prompt\"\n" +
		"cat <<'JSON'\n" + envelope + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func fetchCluster(t *testing.T, base string, repos ...string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Get(base + "/cluster?" + url.Values{"repo": repos}.Encode())
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

func TestServeClustersSkills(t *testing.T) {
	// repoClaudeOnly skills sorted by name: commit (s1), dev (s2), po (s3),
	// pr (s4), review (s5); repoMultiAgent follows with three "fh" skills.
	// s3 and the fh skills are left out and must end up in "Other".
	bin, dir := fakeClaude(t, "```json\n"+`[
		{"name": "Git workflow", "description": "Commits and pull requests", "skills": ["s1", "s4", "s999"]},
		{"name": "Development", "description": "Writing and reviewing code", "skills": ["s2", "s5"]}
	]`+"\n```")
	base := startServe(t, "--claude-bin", bin)

	code, body := fetchCluster(t, base, repoClaudeOnly, repoMultiAgent, "https://github.com/simpletonDL/no-such-repo-atlas", "/etc")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	for _, want := range []string{
		"<h2>Git workflow", "Commits and pull requests", "<h2>Development", "<h2>Other",
		"<h3>commit</h3>", "<h3>review</h3>", "<h3>fh</h3>",
		repoClaudeOnly + " · .claude/skills/commit/SKILL.md",
		"failed to clone", "invalid repository URL",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page has no %q", want)
		}
	}
	if other := strings.Index(body, "<h2>Other"); strings.Index(body, "<h3>po</h3>") < other {
		t.Error("omitted skill po is not in Other")
	}

	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	prompt, _ := os.ReadFile(filepath.Join(dir, "prompt"))
	if strings.TrimSpace(string(args)) != "-p --output-format json" {
		t.Errorf("claude args = %q", args)
	}
	for _, want := range []string{`"id": "s1"`, `"name": "commit"`, `"name": "fh"`} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("prompt has no %q:\n%s", want, prompt)
		}
	}
}

func TestServeClusterErrors(t *testing.T) {
	bin, _ := fakeClaude(t, "Sorry, I can't help with that.")
	base := startServe(t, "--claude-bin", bin)
	code, body := fetchCluster(t, base, repoClaudeOnly)
	if code != http.StatusBadGateway || !strings.Contains(body, "claude returned invalid JSON") {
		t.Errorf("invalid JSON: status = %d, want 502 with an error:\n%s", code, body)
	}

	base = startServe(t, "--claude-bin", filepath.Join(t.TempDir(), "no-claude"))
	code, body = fetchCluster(t, base, repoClaudeOnly)
	if code != http.StatusBadGateway || !strings.Contains(body, "claude CLI not found") {
		t.Errorf("missing claude: status = %d, want 502 with an error:\n%s", code, body)
	}
}
