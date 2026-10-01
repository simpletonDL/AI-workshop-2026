package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCloneStderr(t *testing.T) {
	var got []int
	c := &cloneStderr{report: func(p int) { got = append(got, p) }}
	out := "Cloning into 'repo'...\n" +
		"warning: filtering not recognized by server, ignoring\n" +
		"remote: Enumerating objects: 9, done.        \n" +
		"remote: Counting objects:  50% (4/9)        \rremote: Counting objects: 100% (9/9), done.        \n" +
		"remote: Total 9 (delta 1), reused 0 (delta 0)        \n" +
		"Receiving objects:   0% (0/9)\rReceiving objects:  50% (4/9), 1.00 MiB | 2.00 MiB/s\r" +
		"Receiving objects: 100% (9/9), 2.00 MiB | 2.00 MiB/s, done.\n" +
		"Resolving deltas: 100% (1/1), done.\n" +
		"Updating files:  40% (2/5)\r" +
		"warning: something odd\n" +
		"fatal: early EOF"
	// Git's chunks don't follow line boundaries.
	for len(out) > 0 {
		n := min(7, len(out))
		c.Write([]byte(out[:n]))
		out = out[n:]
	}
	c.flush()
	if want := []int{0, 25, 50, 60, 76}; !reflect.DeepEqual(got, want) {
		t.Errorf("reported %v, want %v", got, want)
	}
	if msg := c.msg.String(); msg != "warning: something odd\nfatal: early EOF\n" {
		t.Errorf("error message = %q", msg)
	}
}

func TestGitCloneReportsProgress(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	writeSkill(t, src, "skills/deploy", "---\nname: deploy\ndescription: Deploy\n---\n")
	if err := os.WriteFile(filepath.Join(src, "skills/deploy/script.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	last := -1
	ctx := withCloneProgress(context.Background(), func(p int) { last = p })
	dest := filepath.Join(t.TempDir(), "repo")
	if err := gitClone(ctx, "file://"+filepath.ToSlash(src), "", dest); err != nil {
		t.Fatal(err)
	}
	if last < cloneFetched {
		t.Errorf("last reported progress = %d, want at least %d (trees fetched)", last, cloneFetched)
	}
	// Only the SKILL.md files are checked out.
	if _, err := os.Stat(filepath.Join(dest, "skills/deploy/SKILL.md")); err != nil {
		t.Errorf("SKILL.md not checked out: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "skills/deploy/script.sh")); !os.IsNotExist(err) {
		t.Errorf("script.sh checked out (err = %v), want a sparse checkout of SKILL.md files", err)
	}

	err := gitClone(context.Background(), "file://"+filepath.ToSlash(filepath.Join(src, "missing")), "", filepath.Join(t.TempDir(), "repo"))
	if err == nil || strings.Contains(err.Error(), "Cloning into") {
		t.Errorf("error = %v, want git's error without progress chatter", err)
	}
}
