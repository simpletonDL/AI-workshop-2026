package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/atlas/internal/skills"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Execute(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeSkill(t *testing.T, root, dir, content string) {
	t.Helper()
	p := filepath.Join(root, dir, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListSkillsTable(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, ".claude/skills/deploy", "---\nname: deploy\ndescription: Deploy the service to staging\n---\n")
	writeSkill(t, root, "skills/code-review", "---\nname: code-review\ndescription: |\n  Review the current diff\n  for correctness bugs\n---\n")

	code, out, errOut := run(t, "list-skills", root)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut)
	}
	want := "NAME         DESCRIPTION\n" +
		"code-review  Review the current diff for correctness bugs\n" +
		"deploy       Deploy the service to staging\n"
	if out != want {
		t.Errorf("stdout =\n%s\nwant\n%s", out, want)
	}
}

func TestListSkillsJSON(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/deploy", "---\nname: deploy\ndescription: Deploy\n---\n")

	code, out, errOut := run(t, "list-skills", "--json", root)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut)
	}
	var got []skills.Skill
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	want := []skills.Skill{{Name: "deploy", Description: "Deploy", Path: "skills/deploy/SKILL.md"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestListSkillsNoSkills(t *testing.T) {
	root := t.TempDir()

	code, out, _ := run(t, "list-skills", root)
	if code != 0 || out != "No skills found\n" {
		t.Errorf("got code=%d out=%q, want 0 and %q", code, out, "No skills found\n")
	}

	code, out, _ = run(t, "list-skills", "--json", root)
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Errorf("json: got code=%d out=%q, want 0 and []", code, out)
	}
}

func TestListSkillsCloneFailure(t *testing.T) {
	requireGit(t)
	code, out, errOut := run(t, "list-skills", filepath.Join(t.TempDir(), "does-not-exist"))
	if code == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "failed to clone") {
		t.Errorf("stderr = %q, want clone error", errOut)
	}
}

func TestListSkillsRequiresArg(t *testing.T) {
	if code, _, _ := run(t, "list-skills"); code == 0 {
		t.Error("exit code = 0, want non-zero")
	}
}

func TestListSkillsGitRef(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	writeSkill(t, repo, "skills/old", "---\nname: old\n---\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "v1")
	git("tag", "v1")
	writeSkill(t, repo, "skills/new", "---\nname: new\n---\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "v2")

	code, out, errOut := run(t, "list-skills", "--json", "--ref", "v1", repo)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut)
	}
	var got []skills.Skill
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "old" {
		t.Errorf("at v1 got %+v, want only 'old'", got)
	}

	code, _, errOut = run(t, "list-skills", "--ref", "no-such-ref", repo)
	if code == 0 {
		t.Errorf("unknown ref: exit code = 0, want non-zero")
	}
	if !strings.Contains(errOut, "failed to clone") {
		t.Errorf("stderr = %q, want clone error", errOut)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}
