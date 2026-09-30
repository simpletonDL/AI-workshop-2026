//go:build integration

// Package integration runs the built atlas binary against real public GitHub
// repositories, covering the whole pipeline: clone, discovery, parsing, output.
//
// The repositories are third-party and not pinned, so assertions check stable
// facts (skills that exist, where they live) rather than exact output.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/example/atlas/internal/skills"
)

// Small repositories (under 1 MB) with skills in different places.
const (
	// The same skill "fh" in .agents/, .claude/ and .cursor/.
	repoMultiAgent = "https://github.com/yaralahruthik/find-me-a-job"
	// The same skill "zig" in skills/ and a dozen dot-dirs (.agent, .pi, ...).
	repoManyDirs = "https://github.com/nzrsky/zig-skills"
	// Several skills, all in .claude/skills.
	repoClaudeOnly = "https://github.com/leandronsp/curupira"
	// .claude/skills/* are symlinks to ../../.agents/skills/*, except "pr",
	// which is a real directory in both places.
	repoSymlinks = "https://github.com/zacharyfmarion/openscad-studio"
)

var atlasBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "atlas-integration-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	atlasBin = filepath.Join(dir, "atlas")
	build := exec.Command("go", "build", "-o", atlasBin, "github.com/example/atlas/cmd/atlas")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build atlas: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func atlas(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, atlasBin, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("atlas %v timed out", args)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run atlas %v: %v", args, err)
	}
	return code, out.String(), errOut.String()
}

func listJSON(t *testing.T, args ...string) []skills.Skill {
	t.Helper()
	code, out, errOut := atlas(t, append([]string{"list-skills", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut)
	}
	var got []skills.Skill
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(got) == 0 {
		t.Fatal("no skills found")
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		if got[i].Name != got[j].Name {
			return got[i].Name < got[j].Name
		}
		return got[i].Path < got[j].Path
	}) {
		t.Errorf("skills are not sorted by name, then path: %+v", got)
	}
	return got
}

func byPath(list []skills.Skill) map[string]skills.Skill {
	m := make(map[string]skills.Skill, len(list))
	for _, s := range list {
		m[s.Path] = s
	}
	return m
}

func requirePaths(t *testing.T, got []skills.Skill, name string, paths ...string) {
	t.Helper()
	m := byPath(got)
	for _, p := range paths {
		s, ok := m[p]
		if !ok {
			t.Errorf("missing %s", p)
			continue
		}
		if s.Name != name {
			t.Errorf("%s: name = %q, want %q", p, s.Name, name)
		}
	}
}

func TestSameSkillInSeveralAgentDirs(t *testing.T) {
	got := listJSON(t, repoMultiAgent)
	requirePaths(t, got, "fh",
		".agents/skills/fh/SKILL.md",
		".claude/skills/fh/SKILL.md",
		".cursor/skills/fh/SKILL.md",
	)
	for _, s := range got {
		if s.Name == "fh" && s.Description == "" {
			t.Errorf("%s: empty description", s.Path)
		}
	}
}

func TestSkillsInUnusualDirs(t *testing.T) {
	got := listJSON(t, repoManyDirs)
	requirePaths(t, got, "zig",
		"skills/zig/SKILL.md",
		".agent/skills/zig/SKILL.md",
		".adal/skills/zig/SKILL.md",
		".pi/skills/zig/SKILL.md",
		".codebuddy/skills/zig/SKILL.md",
	)
}

func TestClaudeSkillsTable(t *testing.T) {
	code, out, errOut := atlas(t, "list-skills", repoClaudeOnly)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if fields := strings.Fields(lines[0]); len(fields) != 2 || fields[0] != "NAME" || fields[1] != "DESCRIPTION" {
		t.Errorf("header = %q, want NAME DESCRIPTION", lines[0])
	}
	names := map[string]bool{}
	for _, l := range lines[1:] {
		names[strings.Fields(l)[0]] = true
	}
	for _, want := range []string{"commit", "dev", "po", "pr", "review"} {
		if !names[want] {
			t.Errorf("table has no %q row:\n%s", want, out)
		}
	}
}

func TestSymlinkedSkillsListedOnce(t *testing.T) {
	got := listJSON(t, repoSymlinks)

	// Symlinked .claude/skills/<name> are not followed: one entry per skill.
	for _, name := range []string{"adapt", "animate", "audit"} {
		var paths []string
		for _, s := range got {
			if s.Name == name {
				paths = append(paths, s.Path)
			}
		}
		if want := ".agents/skills/" + name + "/SKILL.md"; len(paths) != 1 || paths[0] != want {
			t.Errorf("%s: paths = %v, want [%s]", name, paths, want)
		}
	}
	// "pr" is a real directory on both sides, so both copies are listed.
	requirePaths(t, got, "pr", ".agents/skills/pr/SKILL.md", ".claude/skills/pr/SKILL.md")
}

func TestRef(t *testing.T) {
	got := listJSON(t, "--ref", "main", repoMultiAgent)
	requirePaths(t, got, "fh", ".claude/skills/fh/SKILL.md")

	code, out, errOut := atlas(t, "list-skills", "--ref", "no-such-ref-atlas", repoMultiAgent)
	if code == 0 {
		t.Errorf("unknown ref: exit code = 0, stdout = %q", out)
	}
	if !strings.Contains(errOut, "failed to clone") {
		t.Errorf("stderr = %q, want clone error", errOut)
	}
}

func TestRepositoryNotFound(t *testing.T) {
	code, out, errOut := atlas(t, "list-skills", "https://github.com/simpletonDL/no-such-repo-atlas")
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
