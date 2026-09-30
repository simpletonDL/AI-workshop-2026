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
	"regexp"
	"strconv"
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

	args := []string{"clone", "--progress", "--depth", "1", "--single-branch"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	// "--" prevents a source starting with "-" from being parsed as an option.
	args = append(args, "--", source, dest)

	cmd := exec.CommandContext(ctx, "git", args...)
	// Fail instead of hanging on credential prompts.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	stderr := &cloneStderr{report: cloneProgressFrom(ctx)}
	cmd.Stderr = stderr

	err := cmd.Run()
	stderr.flush()
	if err != nil {
		msg := strings.TrimSpace(stderr.msg.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("failed to clone %s: %s", source, msg)
	}
	return nil
}

// cloneProgressKey is the context key of the clone progress callback.
type cloneProgressKey struct{}

// withCloneProgress makes gitClone report the clone's completion (0–100) to
// report. Calls come from one goroutine; percent may repeat but never goes down
// within a phase.
func withCloneProgress(ctx context.Context, report func(percent int)) context.Context {
	return context.WithValue(ctx, cloneProgressKey{}, report)
}

func cloneProgressFrom(ctx context.Context) func(int) {
	if f, ok := ctx.Value(cloneProgressKey{}).(func(int)); ok {
		return f
	}
	return nil
}

// clonePhases maps local phases of `git clone --progress` to a share of the
// whole clone: [from, to) percent. Remote phases (counting, compressing) are
// short and not reported.
var clonePhases = map[string][2]int{
	"Receiving objects": {0, 80},
	"Resolving deltas":  {80, 95},
	"Updating files":    {95, 100},
}

var (
	// cloneProgressLine is a progress line, e.g. "Receiving objects:  45% (4/9), 1 MiB | 2 MiB/s".
	cloneProgressLine = regexp.MustCompile(`^(?:remote: )?([A-Za-z ]+): +(\d+)% \(\d+/\d+\)`)
	// cloneNoise is other chatter that is not part of an error message.
	cloneNoise = regexp.MustCompile(`^(?:Cloning into |remote: (?:Enumerating objects:|Total ))`)
)

// cloneStderr parses the stderr of `git clone --progress`: progress lines go
// to report, the rest is kept in msg for the error message.
type cloneStderr struct {
	report  func(int)
	msg     bytes.Buffer
	pending []byte
}

func (c *cloneStderr) Write(b []byte) (int, error) {
	c.pending = append(c.pending, b...)
	// Progress updates end with \r, finished lines with \n.
	for {
		i := bytes.IndexAny(c.pending, "\r\n")
		if i < 0 {
			break
		}
		c.line(string(c.pending[:i]))
		c.pending = c.pending[i+1:]
	}
	return len(b), nil
}

func (c *cloneStderr) flush() {
	c.line(string(c.pending))
	c.pending = nil
}

func (c *cloneStderr) line(s string) {
	s = strings.TrimSpace(s)
	if s == "" || cloneNoise.MatchString(s) {
		return
	}
	m := cloneProgressLine.FindStringSubmatch(s)
	if m == nil {
		c.msg.WriteString(s + "\n")
		return
	}
	phase, ok := clonePhases[m[1]]
	if !ok || c.report == nil {
		return
	}
	pct, _ := strconv.Atoi(m[2])
	c.report(phase[0] + (phase[1]-phase[0])*min(pct, 100)/100)
}
