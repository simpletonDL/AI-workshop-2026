package cli

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/example/atlas/internal/skills"
)

//go:embed serve.html
var servePageHTML string

var servePage = template.Must(template.New("page").Parse(servePageHTML))

// scanTimeout bounds how long a single request may spend cloning and scanning.
const scanTimeout = 2 * time.Minute

// maxSkillText is how much of a SKILL.md is shown in the UI.
const maxSkillText = 256 << 10

// maxHistory is how many recent searches the UI remembers.
const maxHistory = 10

type serveOptions struct {
	addr          string
	claudeBin     string
	claudeTimeout time.Duration
	cacheDir      string
	cacheTTL      time.Duration
	cacheSize     int
}

func newServeCommand() *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a web UI for listing skills in a git repository",
		Long: `Starts a local web server. Open it in a browser, enter a repository URL
(and optionally a branch or tag) to see the skills it contains.`,
		Example: `  atlas serve
  atlas serve --addr 127.0.0.1:9000
  atlas serve --claude-bin /usr/local/bin/claude
  atlas serve --cache-ttl 1h --cache-size 100
  atlas serve --cache-ttl 0   # clone on every request`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.addr, "addr", "localhost:8080", "listen address (host:port)")
	cmd.Flags().StringVar(&opts.claudeBin, "claude-bin", "claude", "Claude Code CLI used by /cluster")
	cmd.Flags().DurationVar(&opts.claudeTimeout, "claude-timeout", defaultClaudeTimeout, "timeout of one Claude call on /cluster")
	cmd.Flags().StringVar(&opts.cacheDir, "cache-dir", "", "parent directory of the cloned repositories cache (defaults to the system temp directory)")
	cmd.Flags().DurationVar(&opts.cacheTTL, "cache-ttl", defaultCacheTTL, "how long a cloned repository is reused before it is cloned again (0 disables the cache)")
	cmd.Flags().IntVar(&opts.cacheSize, "cache-size", defaultCacheMaxEntries, "maximum number of cached repositories")
	return cmd
}

func runServe(cmd *cobra.Command, opts serveOptions) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if opts.cacheTTL < 0 {
		return errors.New("--cache-ttl must not be negative")
	}
	// One logger for requests, their stages and the repository cache.
	log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	co := checkoutFunc(checkout)
	if opts.cacheTTL > 0 {
		cache, err := newRepoCache(opts.cacheDir, opts.cacheTTL, opts.cacheSize, gitClone, log)
		if err != nil {
			return err
		}
		// Runs after the server has shut down, so no request reads a checkout.
		defer cache.Close()
		co = cache.checkout
	}

	ln, err := net.Listen("tcp", opts.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.addr, err)
	}
	srv := &http.Server{
		Handler: newServeHandler(co,
			withClaude(claudeCLI(opts.claudeBin), opts.claudeTimeout),
			withLogger(log)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Serving atlas on http://%s\n", ln.Addr())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

type checkoutFunc func(ctx context.Context, source, ref string) (dir string, cleanup func(), err error)

type pageData struct {
	Repo     string
	Ref      string
	Filter   string
	Searched bool
	Error    string
	Skills   []skillView // skills whose name matches Filter
	Total    int         // number of skills in the repository before filtering
	Warnings []string
	History  []historyEntry
}

// skillView is a skill together with the text of its SKILL.md.
type skillView struct {
	skills.Skill
	Text      string
	Truncated bool
}

// historyEntry is a search that completed without an error.
type historyEntry struct {
	Repo  string
	Ref   string
	Count int
}

// history keeps the most recent successful searches, newest first.
type history struct {
	mu      sync.Mutex
	entries []historyEntry
}

// add records e, moving an earlier search for the same repo and ref to the top.
func (h *history) add(e historyEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := []historyEntry{e}
	for _, old := range h.entries {
		if old.Repo != e.Repo || old.Ref != e.Ref {
			entries = append(entries, old)
		}
	}
	if len(entries) > maxHistory {
		entries = entries[:maxHistory]
	}
	h.entries = entries
}

func (h *history) list() []historyEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]historyEntry(nil), h.entries...)
}

// newServeHandler returns the web UI handler. GET / shows the form; with a
// ?repo= query it also clones the repository and shows its skills.
// GET /cluster groups skills of several repositories with Claude.
// GET /progress reports the progress of a running page request.
func newServeHandler(checkout checkoutFunc, opts ...serveOption) http.Handler {
	cfg := newServeConfig(opts)
	var recent history
	var jobs progressJobs
	mux := http.NewServeMux()
	mux.HandleFunc("/cluster", serveCluster(checkout, cfg, &jobs))
	mux.HandleFunc("/progress", jobs.serveProgress)
	mux.HandleFunc("/progress.js", serveProgressJS)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		data := pageData{
			Repo:   strings.TrimSpace(r.URL.Query().Get("repo")),
			Ref:    strings.TrimSpace(r.URL.Query().Get("ref")),
			Filter: strings.TrimSpace(r.URL.Query().Get("filter")),
		}
		status := http.StatusOK
		if data.Repo != "" {
			data.Searched = true
			p, done := jobs.start(r.Header.Get(progressHeader))
			status = scan(r.Context(), checkout, &data, p)
			done()
			if data.Error == "" {
				data.Total = len(data.Skills)
				recent.add(historyEntry{Repo: data.Repo, Ref: data.Ref, Count: data.Total})
				data.Skills = filterSkills(data.Skills, data.Filter)
			}
		}
		data.History = recent.list()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		// Headers are already sent, so a template error can't be reported.
		_ = servePage.Execute(w, data)
	})
	return logRequests(cfg.log, mux)
}

// Progress weights of the main page: cloning dominates and advances with git's
// progress, discovery and reading the SKILL.md files are quick.
const (
	scanCloneSteps    = 80
	scanDiscoverSteps = 10
	scanReadSteps     = 10
)

// scan fills data with the skills of data.Repo, reporting its stages to p and
// the log, and returns the HTTP status.
func scan(ctx context.Context, checkout checkoutFunc, data *pageData, p *progress) int {
	log := logFrom(ctx).With("repo", redactRepo(data.Repo), "ref", data.Ref)
	p.setTotal(scanCloneSteps + scanDiscoverSteps + scanReadSteps)
	p.setStage("Validating…")
	log.Info("scan: validating")
	if err := validateRemote(data.Repo); err != nil {
		log.Info("scan: invalid repository URL")
		data.Error = err.Error()
		return http.StatusBadRequest
	}

	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	p.setStage(fmt.Sprintf("Cloning %s…", redactRepo(data.Repo)))
	log.Info("scan: cloning")
	start := time.Now()
	clone := p.part(scanCloneSteps)
	dir, cleanup, err := checkout(withCloneProgress(ctx, clone.set), data.Repo, data.Ref)
	defer cleanup()
	if err != nil {
		log.Warn("scan: clone failed", "duration", time.Since(start).Round(time.Millisecond), "error", redactError(err, data.Repo))
		data.Error = err.Error()
		return http.StatusBadGateway
	}
	log.Info("scan: clone done", "duration", time.Since(start).Round(time.Millisecond))

	clone.finish("Discovering skills…")
	log.Info("scan: discovering skills")
	found, warnings, err := skills.Discover(dir)
	if err != nil {
		log.Warn("scan: discovery failed", "error", err.Error())
		data.Error = fmt.Sprintf("scan repository: %v", err)
		return http.StatusInternalServerError
	}
	log.Info("scan: skills found", "skills", len(found), "warnings", len(warnings))

	p.advance(scanDiscoverSteps, fmt.Sprintf("Reading %d SKILL.md file%s…", len(found), plural(len(found))))
	for _, s := range found {
		view := skillView{Skill: s}
		view.Text, view.Truncated, err = readSkillText(filepath.Join(dir, filepath.FromSlash(s.Path)))
		if err != nil {
			log.Warn("scan: read SKILL.md failed", "path", s.Path, "error", err.Error())
			data.Error = fmt.Sprintf("read %s: %v", s.Path, err)
			return http.StatusInternalServerError
		}
		data.Skills = append(data.Skills, view)
	}
	for _, w := range warnings {
		data.Warnings = append(data.Warnings, w.String())
	}
	p.advance(scanReadSteps, "")
	log.Info("scan: done")
	return http.StatusOK
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// filterSkills returns the skills whose name contains filter, ignoring case.
// An empty filter matches every skill.
func filterSkills(all []skillView, filter string) []skillView {
	if filter == "" {
		return all
	}
	filter = strings.ToLower(filter)
	var matched []skillView
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.Name), filter) {
			matched = append(matched, s)
		}
	}
	return matched
}

// readSkillText returns up to maxSkillText bytes of the file at p.
func readSkillText(p string) (text string, truncated bool, err error) {
	f, err := os.Open(p)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	buf := make([]byte, maxSkillText+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", false, err
	}
	if n > maxSkillText {
		// Drop a multi-byte character cut in half by the limit.
		return strings.ToValidUTF8(string(buf[:maxSkillText]), ""), true, nil
	}
	return string(buf[:n]), false, nil
}

// scpLikeRemote matches ssh remotes such as git@github.com:org/repo.git.
var scpLikeRemote = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/\\].*$`)

// validateRemote accepts only remote repository URLs. Unlike the CLI, the web
// UI must not let visitors scan local paths on the server or use file:// or
// helper transports such as ext::.
func validateRemote(repo string) error {
	for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(strings.ToLower(repo), scheme) && len(repo) > len(scheme) {
			return nil
		}
	}
	if scpLikeRemote.MatchString(repo) {
		return nil
	}
	return fmt.Errorf("invalid repository URL %q: use an https or ssh URL", repo)
}
