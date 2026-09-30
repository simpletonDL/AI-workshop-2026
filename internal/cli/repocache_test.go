package cli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClone writes a marker file instead of cloning and counts its calls.
type fakeClone struct {
	calls atomic.Int32
	err   error
	// started and release, if set, block every clone until release is closed.
	started chan struct{}
	release chan struct{}
}

func (f *fakeClone) clone(ctx context.Context, source, ref, dest string) error {
	n := f.calls.Add(1)
	if f.started != nil {
		f.started <- struct{}{}
		<-f.release
	}
	if f.err != nil {
		return f.err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "clone"), []byte(strings.Repeat("x", int(n))), 0o644)
}

// fakeClock is a manually advanced clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestCache(t *testing.T, ttl time.Duration, size int, clone cloneFunc) (*repoCache, *fakeClock) {
	t.Helper()
	c, err := newRepoCache(t.TempDir(), ttl, size, clone, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c.now = clock.now
	t.Cleanup(func() { _ = c.Close() })
	return c, clock
}

// use checks out source@ref, verifies the checkout exists and releases it.
func use(t *testing.T, c *repoCache, source, ref string) string {
	t.Helper()
	dir, cleanup, err := c.checkout(context.Background(), source, ref)
	if err != nil {
		t.Fatalf("checkout(%q, %q): %v", source, ref, err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(dir, "clone")); err != nil {
		t.Fatalf("checkout dir %s has no clone: %v", dir, err)
	}
	return dir
}

func TestRepoCacheHitAndMiss(t *testing.T) {
	fc := &fakeClone{}
	c, _ := newTestCache(t, time.Minute, 10, fc.clone)

	first := use(t, c, "https://github.com/org/a", "")
	if again := use(t, c, "https://github.com/org/a", ""); again != first {
		t.Errorf("second checkout dir = %s, want cached %s", again, first)
	}
	if n := fc.calls.Load(); n != 1 {
		t.Fatalf("clone called %d times for a repeated request, want 1", n)
	}

	// Equivalent URL spellings share the entry; another ref does not.
	use(t, c, "https://GitHub.com/org/a.git/", "")
	if n := fc.calls.Load(); n != 1 {
		t.Errorf("clone called %d times for an equivalent URL, want 1", n)
	}
	if other := use(t, c, "https://github.com/org/a", "v1"); other == first {
		t.Error("different refs share a checkout")
	}
	use(t, c, "https://github.com/org/b", "")
	if n := fc.calls.Load(); n != 3 {
		t.Errorf("clone called %d times, want 3 (a, a@v1, b)", n)
	}
	if !strings.HasPrefix(first, c.dir+string(filepath.Separator)) {
		t.Errorf("checkout %s is outside the cache dir %s", first, c.dir)
	}
}

func TestRepoCacheTTL(t *testing.T) {
	fc := &fakeClone{}
	c, clock := newTestCache(t, 10*time.Minute, 10, fc.clone)

	first := use(t, c, "https://github.com/org/a", "")
	clock.advance(9 * time.Minute)
	use(t, c, "https://github.com/org/a", "")
	if n := fc.calls.Load(); n != 1 {
		t.Fatalf("clone called %d times before the TTL, want 1", n)
	}

	clock.advance(2 * time.Minute)
	second := use(t, c, "https://github.com/org/a", "")
	if n := fc.calls.Load(); n != 2 {
		t.Fatalf("clone called %d times after the TTL, want 2", n)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("expired checkout %s was not removed", first)
	}
	if text, _ := os.ReadFile(filepath.Join(second, "clone")); string(text) != "xx" {
		t.Errorf("checkout after the TTL is not the new clone: %q", text)
	}
}

func TestRepoCacheEvictsLeastRecentlyUsed(t *testing.T) {
	fc := &fakeClone{}
	c, _ := newTestCache(t, time.Hour, 2, fc.clone)

	a := use(t, c, "https://github.com/org/a", "")
	b := use(t, c, "https://github.com/org/b", "")
	use(t, c, "https://github.com/org/a", "") // a is now more recent than b
	use(t, c, "https://github.com/org/c", "") // evicts b
	c.bg.Wait()

	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Errorf("least recently used checkout %s was not removed", b)
	}
	if _, err := os.Stat(a); err != nil {
		t.Errorf("recently used checkout was removed: %v", err)
	}
	calls := fc.calls.Load()
	use(t, c, "https://github.com/org/a", "")
	if fc.calls.Load() != calls {
		t.Error("recently used repository was cloned again")
	}
	use(t, c, "https://github.com/org/b", "")
	if fc.calls.Load() != calls+1 {
		t.Error("evicted repository was not cloned again")
	}
	if n := len(c.entries); n != 2 {
		t.Errorf("cache has %d entries, want 2", n)
	}
}

func TestRepoCacheEvictionWaitsForReaders(t *testing.T) {
	fc := &fakeClone{}
	c, _ := newTestCache(t, time.Hour, 1, fc.clone)

	a, releaseA, err := c.checkout(context.Background(), "https://github.com/org/a", "")
	if err != nil {
		t.Fatal(err)
	}
	use(t, c, "https://github.com/org/b", "") // evicts a while it is in use
	time.Sleep(20 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(a, "clone")); err != nil {
		t.Fatalf("checkout removed while a request reads it: %v", err)
	}
	releaseA()
	releaseA() // cleanup is idempotent
	c.bg.Wait()
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Errorf("evicted checkout %s was not removed after release", a)
	}
}

func TestRepoCacheRefreshWaitsForReaders(t *testing.T) {
	fc := &fakeClone{}
	c, clock := newTestCache(t, time.Minute, 10, fc.clone)

	dir, release, err := c.checkout(context.Background(), "https://github.com/org/a", "")
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Minute)

	done := make(chan struct{})
	go func() {
		defer close(done)
		use(t, c, "https://github.com/org/a", "")
	}()
	select {
	case <-done:
		t.Fatal("expired checkout refreshed while a request reads it")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(dir, "clone")); err != nil {
		t.Fatalf("checkout changed while a request reads it: %v", err)
	}
	release()
	<-done
	if n := fc.calls.Load(); n != 2 {
		t.Errorf("clone called %d times, want 2", n)
	}
}

func TestRepoCacheConcurrentSameKeyClonesOnce(t *testing.T) {
	fc := &fakeClone{started: make(chan struct{}, 10), release: make(chan struct{})}
	c, _ := newTestCache(t, time.Minute, 10, fc.clone)

	const n = 8
	var wg sync.WaitGroup
	dirs := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir, cleanup, err := c.checkout(context.Background(), "https://github.com/org/a", "main")
			defer cleanup()
			dirs[i], errs[i] = dir, err
		}(i)
	}
	<-fc.started
	time.Sleep(50 * time.Millisecond) // let the other requests queue up
	close(fc.release)
	wg.Wait()

	if got := fc.calls.Load(); got != 1 {
		t.Errorf("clone called %d times for concurrent requests, want 1", got)
	}
	for i := range dirs {
		if errs[i] != nil || dirs[i] != dirs[0] {
			t.Errorf("request %d: dir %q, err %v; want %q", i, dirs[i], errs[i], dirs[0])
		}
	}
}

func TestRepoCacheFailedCloneNotCached(t *testing.T) {
	fc := &fakeClone{err: errors.New("failed to clone: not found")}
	c, _ := newTestCache(t, time.Minute, 10, fc.clone)

	for i := 0; i < 2; i++ {
		_, cleanup, err := c.checkout(context.Background(), "https://github.com/org/nope", "")
		cleanup()
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v, want clone error", err)
		}
	}
	if n := fc.calls.Load(); n != 2 {
		t.Errorf("clone called %d times, want 2 (failures are not cached)", n)
	}
	if len(c.entries) != 0 {
		t.Errorf("failed clone left %d cache entries", len(c.entries))
	}
	if left, _ := os.ReadDir(c.dir); len(left) != 0 {
		t.Errorf("failed clone left files in the cache dir: %v", left)
	}

	fc.err = nil
	use(t, c, "https://github.com/org/nope", "")
}

func TestRepoCacheCloseRemovesCheckouts(t *testing.T) {
	fc := &fakeClone{}
	c, err := newRepoCache(t.TempDir(), time.Minute, 10, fc.clone, nil)
	if err != nil {
		t.Fatal(err)
	}
	use(t, c, "https://github.com/org/a", "")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.dir); !os.IsNotExist(err) {
		t.Errorf("cache dir %s not removed on Close", c.dir)
	}
}

func TestCacheKeyIsSafe(t *testing.T) {
	for _, in := range [][2]string{
		{"https://github.com/org/a", "../../etc"},
		{"../../../tmp/x", ""},
		{"git@github.com:org/a.git", "a/b"},
	} {
		key := cacheKey(in[0], in[1])
		if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
			t.Errorf("cacheKey(%q, %q) = %q, want a hex hash", in[0], in[1], key)
		}
	}
	if cacheKey("https://github.com/org/a", "") == cacheKey("https://github.com/org/a", "main") {
		t.Error("ref is not part of the key")
	}
	if cacheKey("git@GitHub.com:org/a.git", "") != cacheKey("git@github.com:org/a", "") {
		t.Error("scp-like URL spellings are not normalized")
	}
}

// TestRepoCacheWithGit runs the cache against a real local repository.
func TestRepoCacheWithGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	writeSkill(t, src, "skills/deploy", "---\nname: deploy\n---\n")
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--quiet", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	var clones atomic.Int32
	c, _ := newTestCache(t, time.Minute, 10, func(ctx context.Context, source, ref, dest string) error {
		clones.Add(1)
		return gitClone(ctx, "file://"+filepath.ToSlash(src), ref, dest)
	})
	for i := 0; i < 2; i++ {
		dir, cleanup, err := c.checkout(context.Background(), "https://example.com/repo", "main")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "skills/deploy/SKILL.md")); err != nil {
			t.Errorf("checkout has no skill: %v", err)
		}
		cleanup()
	}
	if n := clones.Load(); n != 1 {
		t.Errorf("git clone ran %d times, want 1", n)
	}
}

// TestServeWithCache runs the web UI on top of the cache: cache events go to
// the handler's logger with the request number and without credentials, and a
// cache hit still passes all progress stages up to 100%.
func TestServeWithCache(t *testing.T) {
	var clones atomic.Int32
	clone := func(_ context.Context, _, _, dest string) error {
		clones.Add(1)
		writeSkill(t, dest, "skills/deploy", "---\nname: deploy\ndescription: Deploy\n---\n")
		return nil
	}
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	c, err := newRepoCache(t.TempDir(), time.Minute, 10, clone, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	h := newServeHandler(c.checkout, withLogger(log))

	repo := "https://user:tok3n@github.com/org/repo"
	for i, id := range []string{"cache-progress-0001", "cache-progress-0002"} {
		code, body := getWithProgress(h, query(repo, "v1"), id)
		if code != 200 || !strings.Contains(body, "1 skill found") {
			t.Fatalf("request %d: status = %d, body:\n%s", i, code, body)
		}
		if _, s := getProgress(t, h, id); s.Percent != 100 || !s.Done {
			t.Errorf("request %d: progress after the page = %+v, want 100%% done", i, s)
		}
	}
	if n := clones.Load(); n != 1 {
		t.Errorf("cloned %d times, want 1", n)
	}
	logs := buf.String()
	for _, want := range []string{
		`msg="repo cache miss, cloning" req=1 repo=https://github.com/org/repo ref=v1`,
		// req=2 is the progress poll after the first page.
		`msg="repo cache hit" req=3 repo=https://github.com/org/repo ref=v1`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log has no %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "tok3n") {
		t.Errorf("log leaks credentials:\n%s", logs)
	}
}

// TestRepoCacheLogsEvictions checks that work outside a request (eviction)
// is logged to the cache's own logger.
func TestRepoCacheLogsEvictions(t *testing.T) {
	var buf syncLogBuffer
	fc := &fakeClone{}
	c, err := newRepoCache(t.TempDir(), time.Minute, 1, fc.clone, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	use(t, c, "https://github.com/org/a", "")
	use(t, c, "https://github.com/org/b", "")
	_ = c.Close()
	if !strings.Contains(buf.String(), `msg="repo cache evict"`) {
		t.Errorf("eviction not logged:\n%s", buf.String())
	}
}

// syncLogBuffer is a log buffer safe for concurrent writers.
type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
