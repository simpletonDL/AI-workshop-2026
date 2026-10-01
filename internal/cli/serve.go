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
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/example/atlas/internal/skills"
)

//go:embed serve.html
var servePageHTML string

var servePage = pageTemplate("page", servePageHTML)

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
	Form     repoFormView
	Repos    []repoSpec // repositories of the form
	Filter   string
	Searched bool
	Error    string       // error of the whole request
	Results  []repoResult // outcome per repository
	Skills   []skillView  // skills of all repositories whose name matches Filter
	Total    int          // number of skills before filtering
	Warnings []string
	History  []historyEntry
	Stars    []starEntry
	Back     string // this page, where a star form returns to
}

// Multi reports whether skills of several repositories are shown, so each
// skill names its repository.
func (d pageData) Multi() bool { return len(d.Results) > 1 }

func (d pageData) RepoErrors() []repoResult { return repoErrors(d.Results) }

// Failed reports whether no repository could be scanned.
func (d pageData) Failed() bool { return len(d.RepoErrors()) == len(d.Results) }

// Query is the repository list of the page as a query string.
func (d pageData) Query() template.URL { return repoQuery(d.Repos) }

// skillView is a skill together with its repository and the text of its
// SKILL.md.
type skillView struct {
	skills.Skill
	Repo      repoSpec
	Text      string
	Truncated bool
	Starred   bool
}

// historyEntry is a search that completed without an error.
type historyEntry struct {
	Repos []repoSpec
	Count int
}

func (e historyEntry) Query() template.URL { return repoQuery(e.Repos) }

// history keeps the most recent successful searches, newest first.
type history struct {
	mu      sync.Mutex
	entries []historyEntry
}

// add records e, moving an earlier search for the same repositories to the top.
func (h *history) add(e historyEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := []historyEntry{e}
	for _, old := range h.entries {
		if !slices.Equal(old.Repos, e.Repos) {
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

// newServeHandler returns the web UI handler. GET / shows the form; with
// ?repo= queries it also clones the repositories and shows their skills.
// GET /cluster groups skills of several repositories with Claude.
// GET /progress reports the progress of a running page request.
// POST /star gives a skill a banana (a star) or takes it back.
func newServeHandler(checkout checkoutFunc, opts ...serveOption) http.Handler {
	cfg := newServeConfig(opts)
	var recent history
	var starred stars
	var jobs progressJobs
	mux := http.NewServeMux()
	mux.HandleFunc("/cluster", serveCluster(checkout, cfg, &jobs))
	mux.HandleFunc("/progress", jobs.serveProgress)
	mux.HandleFunc("/progress.js", serveProgressJS)
	mux.HandleFunc("/repos.js", serveReposJS)
	mux.HandleFunc("/dancer.js", serveDancerJS)
	mux.HandleFunc("/star", serveStar(checkout, &starred))
	mux.HandleFunc("/stars.js", serveStarsJS)
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

		q := r.URL.Query()
		rows, edit := readRepoRows(q)
		data := pageData{Repos: rows, Form: newRepoFormView(rows), Filter: strings.TrimSpace(q.Get("filter")), Back: r.URL.RequestURI()}
		status := http.StatusOK
		if specs, bad := validateRepos(rows); !edit && len(specs)+len(bad) > 0 {
			data.Searched = true
			p, done := jobs.start(r.Header.Get(progressHeader))
			status = scan(r.Context(), checkout, &data, specs, bad, p)
			done()
			if data.Error == "" && len(data.RepoErrors()) == 0 {
				recent.add(historyEntry{Repos: data.Repos, Count: data.Total})
			}
			data.Skills = filterSkills(data.Skills, data.Filter)
			starred.mark(data.Skills)
		}
		data.History = recent.list()
		data.Stars = starred.list()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		// Headers are already sent, so a template error can't be reported.
		_ = servePage.Execute(w, data)
	})
	return logRequests(cfg.log, mux)
}

// mainScan weighs the work on one repository of the main page: cloning
// dominates and advances with git's progress, discovery and reading the
// SKILL.md files are quick.
var mainScan = scanPlan{log: "scan", cloneSteps: 80, discoverSteps: 20, readText: true}

// scan fills data with the skills of the repositories, reporting its stages to
// p and the log, and returns the HTTP status: an error status only if no
// repository could be scanned.
func scan(ctx context.Context, checkout checkoutFunc, data *pageData, specs []repoSpec, bad []repoResult, p *progress) int {
	log := logFrom(ctx)
	p.setTotal((mainScan.cloneSteps + mainScan.discoverSteps) * len(specs))
	p.setStage("Validating…")
	log.Info("scan: validating")
	if n := len(specs) + len(bad); n > maxRepos {
		log.Info("scan: too many repositories", "repos", n)
		data.Error = fmt.Sprintf("too many repositories (%d): at most %d are allowed", n, maxRepos)
		return http.StatusBadRequest
	}
	log.Info("scan: repositories parsed", "valid", len(specs), "invalid", len(bad))

	data.Results = append(scanRepos(ctx, checkout, specs, mainScan, p), bad...)
	for _, res := range data.Results {
		data.Skills = append(data.Skills, res.skills...)
		for _, w := range res.warnings {
			if data.Multi() {
				w = res.Repo.String() + ": " + w
			}
			data.Warnings = append(data.Warnings, w)
		}
	}
	// Each repository is sorted by name, then path; merged, ties keep the
	// repository order.
	sort.SliceStable(data.Skills, func(i, j int) bool { return data.Skills[i].Name < data.Skills[j].Name })
	data.Total = len(data.Skills)
	log.Info("scan: done", "skills", data.Total)
	if data.Failed() {
		return data.Results[0].status
	}
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
