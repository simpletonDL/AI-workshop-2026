package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Defaults of the `atlas serve` repository cache.
const (
	defaultCacheTTL        = 10 * time.Minute
	defaultCacheMaxEntries = 50
)

// cloneFunc shallow-clones source at ref into dest. gitClone in production,
// a fake in tests.
type cloneFunc func(ctx context.Context, source, ref, dest string) error

// repoCache keeps shallow clones of remote repositories for `atlas serve`, so
// repeated requests for the same repository and ref don't clone it again.
//
//   - Entries are keyed by (normalized URL, ref); the directory name is a hash
//     of the key, so user input never becomes a path.
//   - A checkout older than ttl is re-cloned on the next request.
//   - At most maxEntries repositories are kept; the least recently used one is
//     evicted when a new one is added.
//   - Only one request clones a key at a time; the others wait and reuse its
//     result. A checkout is not re-cloned or removed while a request is
//     reading it: readers hold the entry's read lock until cleanup.
//   - Failed clones are not cached.
type repoCache struct {
	dir        string // per-process directory holding all checkouts
	ttl        time.Duration
	maxEntries int
	clone      cloneFunc
	now        func() time.Time
	log        *slog.Logger // the server's logger, for work outside a request

	mu      sync.Mutex
	entries map[string]*cacheEntry
	tick    uint64 // LRU clock
	closed  bool
	bg      sync.WaitGroup // background evictions
}

type cacheEntry struct {
	key      string
	lastUsed uint64 // guarded by repoCache.mu

	mu      sync.RWMutex // write: clone/remove; read: a request uses repo
	root    string       // temp directory with the checkout, "" if none
	repo    string       // checkout directory inside root
	fetched time.Time
	err     error // clone error shared with requests that waited for it
}

// newRepoCache creates the cache in a new directory under parent (the system
// temp directory if empty). Close removes that directory. The cache logs to
// the request-scoped logger of logRequests, or to log (nil: nothing is logged)
// outside a request.
func newRepoCache(parent string, ttl time.Duration, maxEntries int, clone cloneFunc, log *slog.Logger) (*repoCache, error) {
	if ttl <= 0 {
		return nil, errors.New("cache TTL must be positive")
	}
	if maxEntries < 1 {
		return nil, errors.New("cache size must be at least 1")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if parent == "" {
		parent = os.TempDir()
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	// A directory per process: servers sharing --cache-dir never touch each
	// other's checkouts, and Close only removes what this process created.
	dir, err := os.MkdirTemp(parent, "atlas-cache-*")
	if err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	return &repoCache{
		dir:        dir,
		ttl:        ttl,
		maxEntries: maxEntries,
		clone:      clone,
		now:        time.Now,
		log:        log,
		entries:    map[string]*cacheEntry{},
	}, nil
}

// cacheKey derives a file-name-safe key from the repository URL and ref.
func cacheKey(source, ref string) string {
	sum := sha256.Sum256([]byte(normalizeRepoURL(source) + "\x00" + ref))
	return hex.EncodeToString(sum[:])
}

// normalizeRepoURL makes equivalent spellings of a remote URL share a cache
// entry: scheme and host are lowercased, trailing slashes and ".git" dropped.
func normalizeRepoURL(source string) string {
	s := strings.TrimSpace(source)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" && u.Host != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		s = u.String()
	} else if at, colon := strings.Index(s, "@"), strings.Index(s, ":"); at >= 0 && colon > at {
		// scp-like user@host:path
		s = s[:at+1] + strings.ToLower(s[at+1:colon]) + s[colon:]
	}
	s = strings.TrimRight(s, "/")
	return strings.TrimSuffix(s, ".git")
}

// checkout implements checkoutFunc on top of the cache. The returned cleanup
// releases the checkout; it does not remove it.
func (c *repoCache) checkout(ctx context.Context, source, ref string) (string, func(), error) {
	key := cacheKey(source, ref)
	for {
		e, ok := c.lookup(key)
		if !ok {
			// Closed: behave like the uncached CLI.
			return checkout(ctx, source, ref)
		}

		e.mu.RLock()
		if e.err == nil && e.root != "" && c.fresh(e) && c.current(e) {
			c.logger(ctx).Info("repo cache hit", "repo", redactRepo(source), "ref", ref)
			return e.repo, sync.OnceFunc(e.mu.RUnlock), nil
		}
		e.mu.RUnlock()

		retry, err := c.fill(ctx, e, source, ref)
		if err != nil {
			return "", func() {}, err
		}
		if retry {
			continue
		}
		e.mu.RLock()
		if e.root != "" && c.current(e) {
			return e.repo, sync.OnceFunc(e.mu.RUnlock), nil
		}
		// Evicted right after the clone; start over.
		e.mu.RUnlock()
	}
}

// logger returns the logger of the request running ctx, or the cache's own.
func (c *repoCache) logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return c.log
}

// fill clones e unless another request already did it while we waited.
// retry reports that e was evicted and must be looked up again.
func (c *repoCache) fill(ctx context.Context, e *cacheEntry, source, ref string) (retry bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.err != nil {
		// The request we waited for failed; share its error.
		return false, e.err
	}
	if !c.current(e) {
		return true, nil
	}
	if e.root != "" {
		if c.fresh(e) {
			return false, nil
		}
		c.logger(ctx).Info("repo cache expired, cloning again", "repo", redactRepo(source), "ref", ref)
		c.removeCheckout(e)
	} else {
		c.logger(ctx).Info("repo cache miss, cloning", "repo", redactRepo(source), "ref", ref)
	}

	root, err := os.MkdirTemp(c.dir, e.key[:16]+"-*")
	if err != nil {
		c.drop(e)
		return false, fmt.Errorf("create temp dir: %w", err)
	}
	repo := filepath.Join(root, "repo")
	if err := c.clone(ctx, source, ref, repo); err != nil {
		_ = os.RemoveAll(root)
		c.drop(e)
		// Don't hand our own cancellation or timeout to other requests.
		if ctx.Err() == nil {
			e.err = err
		}
		return false, err
	}
	e.root, e.repo, e.fetched = root, repo, c.now()
	return false, nil
}

// lookup returns the entry for key, creating it (and evicting the least
// recently used entry if the cache is full). ok is false after Close.
func (c *repoCache) lookup(key string) (e *cacheEntry, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false
	}
	c.tick++
	if e = c.entries[key]; e != nil {
		e.lastUsed = c.tick
		return e, true
	}
	e = &cacheEntry{key: key, lastUsed: c.tick}
	c.entries[key] = e
	for len(c.entries) > c.maxEntries {
		var oldest *cacheEntry
		for _, o := range c.entries {
			if o != e && (oldest == nil || o.lastUsed < oldest.lastUsed) {
				oldest = o
			}
		}
		delete(c.entries, oldest.key)
		c.bg.Add(1)
		go func() {
			defer c.bg.Done()
			c.evict(oldest)
		}()
	}
	return e, true
}

// evict removes the checkout of an entry already dropped from the map,
// waiting for the requests that are reading it.
func (c *repoCache) evict(e *cacheEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.root != "" {
		c.log.Info("repo cache evict", "dir", e.root)
		c.removeCheckout(e)
	}
}

// removeCheckout deletes e's checkout. The caller holds e.mu for writing.
func (c *repoCache) removeCheckout(e *cacheEntry) {
	_ = os.RemoveAll(e.root)
	e.root, e.repo = "", ""
}

// current reports whether e is still the cache's entry for its key.
func (c *repoCache) current(e *cacheEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[e.key] == e
}

// drop removes e from the map if it is still there.
func (c *repoCache) drop(e *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[e.key] == e {
		delete(c.entries, e.key)
	}
}

func (c *repoCache) fresh(e *cacheEntry) bool {
	return c.now().Sub(e.fetched) < c.ttl
}

// Close removes all checkouts and the cache directory, waiting for requests
// that still read them. Later checkouts are not cached.
func (c *repoCache) Close() error {
	c.mu.Lock()
	c.closed = true
	entries := c.entries
	c.entries = map[string]*cacheEntry{}
	c.mu.Unlock()

	for _, e := range entries {
		c.evict(e)
	}
	c.bg.Wait()
	return os.RemoveAll(c.dir)
}
