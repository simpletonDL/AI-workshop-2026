package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// checkout prepares a directory with the repository contents and returns it
// together with a cleanup function that must always be called.
//
// A local directory without ref is scanned in place. Anything else is
// shallow-cloned into a temporary directory.
func checkout(ctx context.Context, source, ref string) (dir string, cleanup func(), err error) {
	noop := func() {}

	if info, statErr := os.Stat(source); statErr == nil && info.IsDir() {
		if ref == "" {
			return source, noop, nil
		}
		// git ignores --depth for plain local paths; file:// makes it honor it.
		abs, err := filepath.Abs(source)
		if err != nil {
			return "", noop, err
		}
		source = (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
	}

	tmp, err := os.MkdirTemp("", "atlas-*")
	if err != nil {
		return "", noop, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	dest := filepath.Join(tmp, "repo")
	if err := gitClone(ctx, source, ref, dest); err != nil {
		cleanup()
		return "", noop, err
	}
	return dest, cleanup, nil
}

func gitClone(ctx context.Context, source, ref, dest string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed or not in PATH")
	}

	args := []string{"clone", "--quiet", "--depth", "1", "--single-branch"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	// "--" prevents a source starting with "-" from being parsed as an option.
	args = append(args, "--", source, dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	// Fail instead of hanging on credential prompts.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("failed to clone %s: %s", source, msg)
	}
	return nil
}
