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
	"net/url"
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

// orgScanTimeout is scanTimeout for a request that scans an organization.
const orgScanTimeout = 5 * time.Minute

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
	githubAPI     string
	orgMaxRepos   int
}

func newServeCommand() *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a web UI for listing skills in a git repository",
		Long: `Starts a local web server. Open it in a browser, enter a repository URL
(and optionally a branch or tag) or a GitHub organization to see the skills
it contains.

Organizations are listed with the GitHub API; set GITHUB_TOKEN (or GH_TOKEN)
to raise its rate limit from 60 to 5000 calls per hour.`,
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
	cmd.Flags().StringVar(&opts.githubAPI, "github-api", defaultGitHubAPI, "GitHub API used to list organizations (GitHub Enterprise: https://<host>/api/v3)")
	cmd.Flags().IntVar(&opts.orgMaxRepos, "org-max-repos", defaultOrgMaxRepos, "maximum number of repositories scanned per organization (most recently pushed first)")
	return cmd
}

func runServe(cmd *cobra.Command, opts serveOptions) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if opts.cacheTTL < 0 {
		return errors.New("--cache-ttl must not be negative")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	orgs, err := newGitHubOrgs(opts.githubAPI, token, opts.orgMaxRepos, opts.cacheTTL)
	if err != nil {
		return err
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
			withGitHub(orgs),
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
	Org      string     // GitHub organization of the form
	Filter   string
	Searched bool
	Error    string       // error of the whole request
	Results  []repoResult // outcome per repository
	Skills   []skillView  // skills of all repositories whose name matches Filter
	Total    int          // number of skills before filtering
	Warnings []string
	History  []historyEntry

	OrgTruncated bool // only the OrgMax most recently pushed repositories were scanned
	OrgMax       int
}

// Multi reports whether skills of several repositories (or of an
// organization) are shown, so each skill names its repository.
func (d pageData) Multi() bool { return len(d.Results) > 1 || d.Org != "" }

func (d pageData) RepoErrors() []repoResult { return repoErrors(d.Results) }

// Failed reports whether no repository could be scanned.
func (d pageData) Failed() bool {
	n := len(d.RepoErrors())
	return n > 0 && n == len(d.Results)
}

// Query is the repository list and organization of the page as a query
// string.
func (d pageData) Query() template.URL { return pageQuery(d.Repos, d.Org) }

// RepoQuery is the repository list alone, for the /cluster link.
func (d pageData) RepoQuery() template.URL { return repoQuery(d.Repos) }

// WithSkills is the number of scanned repositories that have skills.
func (d pageData) WithSkills() int {
	n := 0
	for _, r := range d.Results {
		if r.Count > 0 {
			n++
		}
	}
	return n
}

// Title is the page title without the "atlas" suffix.
func (d pageData) Title() string {
	var urls []string
	for _, r := range d.Repos {
		if r.URL != "" { // a new blank row of the form
			urls = append(urls, r.URL)
		}
	}
	n := len(urls)
	repos := fmt.Sprintf("%d repositories", n)
	if n == 1 {
		repos = urls[0]
	}
	switch {
	case d.Org != "" && n > 0:
		return d.Org + " organization + " + repos
	case d.Org != "":
		return d.Org + " organization"
	case n > 0:
		return repos
	}
	return ""
}

// pageQuery returns the query of the main page for rows and org.
func pageQuery(rows []repoSpec, org string) template.URL {
	q := repoQuery(rows)
	if org == "" {
		return q
	}
	if q != "" {
		q += "&"
	}
	return q + template.URL("org="+url.QueryEscape(org))
}

// skillView is a skill together with its repository and the text of its
// SKILL.md.
type skillView struct {
	skills.Skill
	Repo      repoSpec
	Text      string
	Truncated bool
}

// historyEntry is a search that completed without an error.
type historyEntry struct {
	Repos []repoSpec
	Org   string
	Count int
}

func (e historyEntry) Query() template.URL { return pageQuery(e.Repos, e.Org) }

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
		if !slices.Equal(old.Repos, e.Repos) || old.Org != e.Org {
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
func newServeHandler(checkout checkoutFunc, opts ...serveOption) http.Handler {
	cfg := newServeConfig(opts)
	var recent history
	var jobs progressJobs
	mux := http.NewServeMux()
	mux.HandleFunc("/cluster", serveCluster(checkout, cfg, &jobs))
	mux.HandleFunc("/progress", jobs.serveProgress)
	mux.HandleFunc("/progress.js", serveProgressJS)
	mux.HandleFunc("/repos.js", serveReposJS)
	mux.HandleFunc("/dancer.js", serveDancerJS)
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
		data := pageData{Repos: rows, Form: newRepoFormView(rows), Org: strings.TrimSpace(q.Get("org")),
			Filter: strings.TrimSpace(q.Get("filter"))}
		status := http.StatusOK
		if specs, bad := validateRepos(rows); !edit && (len(specs)+len(bad) > 0 || data.Org != "") {
			data.Searched = true
			p, done := jobs.start(r.Header.Get(progressHeader))
			status = scan(r.Context(), checkout, cfg.orgs, &data, specs, bad, p)
			done()
			if data.Error == "" && len(data.RepoErrors()) == 0 {
				recent.add(historyEntry{Repos: data.Repos, Org: data.Org, Count: data.Total})
			}
			data.Skills = filterSkills(data.Skills, data.Filter)
		}
		data.History = recent.list()

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

// scan fills data with the skills of the repositories and of the repositories
// of data.Org, reporting its stages to p and the log, and returns the HTTP
// status: an error status only if no repository could be scanned.
func scan(ctx context.Context, checkout checkoutFunc, orgs *githubOrgs, data *pageData, specs []repoSpec, bad []repoResult, p *progress) int {
	log := logFrom(ctx)
	p.setStage("Validating…")
	log.Info("scan: validating")
	if n := len(specs) + len(bad); n > maxRepos {
		log.Info("scan: too many repositories", "repos", n)
		data.Error = fmt.Sprintf("too many repositories (%d): at most %d are allowed", n, maxRepos)
		return http.StatusBadRequest
	}
	log.Info("scan: repositories parsed", "valid", len(specs), "invalid", len(bad))

	plan := mainScan
	var empty []repoResult
	if data.Org != "" {
		var status int
		specs, empty, status = listOrg(ctx, orgs, data, specs, p)
		if status != http.StatusOK {
			return status
		}
		plan.timeout = orgScanTimeout
	}
	p.setTotal((plan.cloneSteps + plan.discoverSteps) * len(specs))

	data.Results = append(append(scanRepos(ctx, checkout, specs, plan, p), empty...), bad...)
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

// listOrg adds the repositories of data.Org to specs, skipping those already
// listed. Empty repositories are not cloned: they are returned as results
// without skills.
func listOrg(ctx context.Context, orgs *githubOrgs, data *pageData, specs []repoSpec, p *progress) (_ []repoSpec, empty []repoResult, status int) {
	log := logFrom(ctx)
	name, err := orgs.parseOrg(data.Org)
	if err != nil {
		log.Info("scan: invalid organization", "error", err.Error())
		data.Error = err.Error()
		return nil, nil, http.StatusBadRequest
	}
	// The form, links and history show the bare name, whatever was typed.
	data.Org = name
	p.setStage(fmt.Sprintf("Listing repositories of %s…", name))
	log.Info("scan: listing organization", "org", name)
	start := time.Now()
	repos, truncated, err := orgs.list(ctx, name)
	if err != nil {
		log.Warn("scan: organization listing failed", "org", name, "error", err.Error())
		data.Error = err.Error()
		if errors.Is(err, errOrgNotFound) {
			return nil, nil, http.StatusNotFound
		}
		return nil, nil, http.StatusBadGateway
	}
	data.OrgTruncated, data.OrgMax = truncated, orgs.max

	seen := map[string]bool{}
	for _, s := range specs {
		seen[cacheKey(s.URL, s.Ref)] = true
	}
	for _, r := range repos {
		spec := repoSpec{URL: r.URL}
		if validateRemote(r.URL) != nil || seen[cacheKey(spec.URL, "")] {
			continue
		}
		seen[cacheKey(spec.URL, "")] = true
		if r.Size == 0 {
			empty = append(empty, repoResult{Repo: spec})
			continue
		}
		specs = append(specs, spec)
	}
	log.Info("scan: organization listed", "org", name, "repos", len(repos), "empty", len(empty),
		"truncated", truncated, "duration", time.Since(start).Round(time.Millisecond))
	return specs, empty, http.StatusOK
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
