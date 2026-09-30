package cli

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/atlas/internal/skills"
)

//go:embed cluster.html
var clusterPageHTML string

var clusterPage = pageTemplate("cluster", clusterPageHTML)

// defaultClaudeTimeout bounds a single `claude -p` call.
const defaultClaudeTimeout = 3 * time.Minute

// maxPromptDescription is how much of each skill description is sent to Claude.
const maxPromptDescription = 500

// otherClusterName is the cluster that collects skills the model left out.
const otherClusterName = "Other"

// claudeRunner sends prompt to Claude and returns the model's text answer.
// It is injected into the handler so tests never call the real Claude.
type claudeRunner func(ctx context.Context, prompt string) (string, error)

// serveOption configures newServeHandler.
type serveOption func(*serveConfig)

type serveConfig struct {
	claude        claudeRunner
	claudeTimeout time.Duration
	log           *slog.Logger
}

// withLogger sets the logger of the web service. Without it nothing is
// logged; `atlas serve` logs to stderr.
func withLogger(log *slog.Logger) serveOption {
	return func(c *serveConfig) { c.log = log }
}

// withClaude sets the runner used by /cluster and the timeout of one call.
func withClaude(run claudeRunner, timeout time.Duration) serveOption {
	return func(c *serveConfig) {
		c.claude = run
		if timeout > 0 {
			c.claudeTimeout = timeout
		}
	}
}

func newServeConfig(opts []serveOption) serveConfig {
	cfg := serveConfig{
		claude:        claudeCLI("claude"),
		claudeTimeout: defaultClaudeTimeout,
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// errClaudeNotFound is returned when the claude binary cannot be found.
var errClaudeNotFound = errors.New("claude CLI not found")

// claudeCLI runs `<bin> -p --output-format json` with the prompt on stdin and
// returns the "result" field of the JSON envelope Claude Code prints.
func claudeCLI(bin string) claudeRunner {
	return func(ctx context.Context, prompt string) (string, error) {
		path, err := exec.LookPath(bin)
		if err != nil {
			return "", fmt.Errorf("%w (%q): install Claude Code or pass --claude-bin", errClaudeNotFound, bin)
		}
		cmd := exec.CommandContext(ctx, path, "-p", "--output-format", "json")
		cmd.Stdin = strings.NewReader(prompt)
		// Run outside the server's working directory so no project context
		// (CLAUDE.md etc.) leaks into the prompt.
		cmd.Dir = os.TempDir()
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("claude timed out: %w", ctx.Err())
			}
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = strings.TrimSpace(stdout.String())
			}
			if msg == "" {
				msg = err.Error()
			}
			return "", fmt.Errorf("claude failed: %s", truncate(msg, 500))
		}
		return parseClaudeEnvelope(stdout.Bytes())
	}
}

// parseClaudeEnvelope extracts the model answer from `--output-format json`
// output. Output that is not an envelope is returned as is.
func parseClaudeEnvelope(out []byte) (string, error) {
	var env struct {
		Type    string  `json:"type"`
		IsError bool    `json:"is_error"`
		Result  *string `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &env); err != nil || env.Result == nil {
		return string(out), nil
	}
	if env.IsError {
		return "", fmt.Errorf("claude returned an error: %s", truncate(*env.Result, 500))
	}
	return *env.Result, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

// parseRepoList parses "<url>" or "<url> <ref>" lines of the former /cluster
// textarea (?repos=, kept for old links), skipping blank lines and dropping
// duplicates. Malformed lines are returned as per-repo errors.
func parseRepoList(text string) (specs []repoSpec, bad []repoResult) {
	seen := map[repoSpec]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		spec := repoSpec{URL: fields[0]}
		if len(fields) > 1 {
			spec.Ref = fields[1]
		}
		if seen[spec] {
			continue
		}
		seen[spec] = true
		if len(fields) > 2 {
			bad = append(bad, repoResult{Repo: spec, Error: fmt.Sprintf("invalid line %q: use \"<url>\" or \"<url> <ref>\"", strings.TrimSpace(line)), status: http.StatusBadRequest})
			continue
		}
		specs = append(specs, spec)
	}
	valid, invalid := validateRepos(specs)
	return valid, append(bad, invalid...)
}

// repoResult is the outcome of scanning one repository.
type repoResult struct {
	Repo     repoSpec
	Count    int
	Error    string
	status   int // HTTP status of the error
	skills   []skillView
	warnings []string
}

// clusterSkill is a skill from one of the scanned repositories.
type clusterSkill struct {
	skills.Skill
	ID   string
	Repo repoSpec
}

type skillCluster struct {
	Name        string
	Description string
	Skills      []clusterSkill
}

type clusterPageData struct {
	Form      repoFormView
	Repos     []repoSpec // repositories of the form, for links to the main page
	Submitted bool
	Error     string
	Results   []repoResult
	Total     int
	Clusters  []skillCluster
}

func (d clusterPageData) RepoErrors() []repoResult { return repoErrors(d.Results) }

// Query is the repository list of the page as a query string.
func (d clusterPageData) Query() template.URL { return repoQuery(d.Repos) }

func repoErrors(results []repoResult) []repoResult {
	var out []repoResult
	for _, r := range results {
		if r.Error != "" {
			out = append(out, r)
		}
	}
	return out
}

// serveCluster handles GET /cluster?repo=<url>&ref=<ref>&repo=…, and the
// former ?repos=<one repo per line>.
func serveCluster(checkout checkoutFunc, cfg serveConfig, jobs *progressJobs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		var data clusterPageData
		var specs []repoSpec
		var bad []repoResult
		edit := false
		if lines := strings.TrimSpace(q.Get("repos")); lines != "" {
			specs, bad = parseRepoList(lines)
			data.Repos = append(data.Repos, specs...)
			for _, b := range bad {
				data.Repos = append(data.Repos, b.Repo)
			}
		} else {
			data.Repos, edit = readRepoRows(q)
			specs, bad = validateRepos(data.Repos)
		}
		data.Form = newRepoFormView(data.Repos)
		status := http.StatusOK
		if !edit && len(specs)+len(bad) > 0 {
			data.Submitted = true
			p, done := jobs.start(r.Header.Get(progressHeader))
			status = clusterRepos(r.Context(), checkout, cfg, &data, specs, bad, p)
			done()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		// Headers are already sent, so a template error can't be reported.
		_ = clusterPage.Execute(w, data)
	}
}

// Progress weights of /cluster per repository: cloning (advancing with git's
// progress) and discovery weigh the same; the Claude call weighs as much as all
// repositories together.
const (
	clusterCloneSteps    = 100
	clusterDiscoverSteps = 100
	clusterStepsPerRepo  = clusterCloneSteps + clusterDiscoverSteps
)

var clusterScan = scanPlan{log: "cluster", cloneSteps: clusterCloneSteps, discoverSteps: clusterDiscoverSteps}

// clusterRepos scans the repositories, clusters their skills with Claude,
// reporting its stages to p and the log, and returns the HTTP status.
func clusterRepos(ctx context.Context, checkout checkoutFunc, cfg serveConfig, data *clusterPageData, specs []repoSpec, bad []repoResult, p *progress) int {
	log := logFrom(ctx)
	p.setStage("Validating…")
	log.Info("cluster: validating")
	if n := len(specs) + len(bad); n > maxRepos {
		log.Info("cluster: too many repositories", "repos", n)
		data.Error = fmt.Sprintf("too many repositories (%d): at most %d are allowed", n, maxRepos)
		return http.StatusBadRequest
	}
	log.Info("cluster: repositories parsed", "valid", len(specs), "invalid", len(bad))
	// The second half of the bar is the Claude call.
	p.setTotal(2 * clusterStepsPerRepo * len(specs))

	data.Results = append(scanRepos(ctx, checkout, specs, clusterScan, p), bad...)
	var all []clusterSkill
	for _, res := range data.Results {
		for _, s := range res.skills {
			all = append(all, clusterSkill{Skill: s.Skill, ID: fmt.Sprintf("s%d", len(all)+1), Repo: res.Repo})
		}
	}
	data.Total = len(all)
	log.Info("cluster: skills found", "skills", len(all))
	if len(specs) == 0 {
		data.Error = "no valid repositories to scan"
		return http.StatusBadRequest
	}
	if len(all) == 0 {
		return http.StatusOK
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.claudeTimeout)
	defer cancel()
	p.setStage(fmt.Sprintf("Clustering %d skill%s with Claude…", len(all), plural(len(all))))
	log.Info("cluster: calling Claude", "skills", len(all))
	start := time.Now()
	answer, err := cfg.claude(ctx, clusterPrompt(all))
	if err != nil {
		log.Warn("cluster: Claude failed", "duration", time.Since(start).Round(time.Millisecond), "error", err.Error())
		data.Error = err.Error()
		return http.StatusBadGateway
	}
	data.Clusters, err = parseClusters(answer, all)
	if err != nil {
		log.Warn("cluster: invalid Claude answer", "duration", time.Since(start).Round(time.Millisecond), "error", truncate(err.Error(), 200))
		data.Error = err.Error()
		return http.StatusBadGateway
	}
	log.Info("cluster: Claude done", "duration", time.Since(start).Round(time.Millisecond), "clusters", len(data.Clusters))
	return http.StatusOK
}

// scanPlan tells scanRepos how to log and weigh the work on one repository.
type scanPlan struct {
	log           string // prefix of the log messages: "scan" or "cluster"
	cloneSteps    int
	discoverSteps int  // discovery and, with readText, reading the SKILL.md files
	readText      bool // also read the text of every SKILL.md
}

// scanRepos clones and scans the repositories in parallel, keeping their
// order. Each repository adds plan.cloneSteps+plan.discoverSteps to p.
func scanRepos(ctx context.Context, checkout checkoutFunc, specs []repoSpec, plan scanPlan, p *progress) []repoResult {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	log := logFrom(ctx)

	n := len(specs)
	if n == 1 {
		p.setStage(fmt.Sprintf("Cloning %s…", redactRepo(specs[0].URL)))
	} else {
		p.setStage(fmt.Sprintf("Cloning %d repositories…", n))
	}
	var mu sync.Mutex
	cloned, scanned := 0, 0
	// cloneDone and scanDone record a finished clone or discovery of repo and
	// move the stage on.
	cloneDone := func(repo string, clone *part) {
		mu.Lock()
		defer mu.Unlock()
		cloned++
		clone.finish(fmt.Sprintf("Cloned %s (%d/%d), discovering skills…", repo, cloned, n))
	}
	scanDone := func(repo string) {
		mu.Lock()
		defer mu.Unlock()
		scanned++
		p.advance(plan.discoverSteps, fmt.Sprintf("Scanned %s (%d/%d)…", repo, scanned, n))
	}

	results := make([]repoResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec repoSpec) {
			defer wg.Done()
			repo := redactRepo(spec.URL)
			log := log.With("repo", repo, "ref", spec.Ref)
			log.Info(plan.log + ": cloning")
			start := time.Now()
			clone := p.part(plan.cloneSteps)
			dir, cleanup, err := checkout(withCloneProgress(ctx, clone.set), spec.URL, spec.Ref)
			defer cleanup()
			if err != nil {
				log.Warn(plan.log+": clone failed", "duration", time.Since(start).Round(time.Millisecond), "error", redactError(err, spec.URL))
				results[i] = repoResult{Repo: spec, Error: err.Error(), status: http.StatusBadGateway}
				cloneDone(repo, clone)
				scanDone(repo)
				return
			}
			log.Info(plan.log+": clone done", "duration", time.Since(start).Round(time.Millisecond))
			cloneDone(repo, clone)
			results[i] = scanCheckout(log, dir, spec, plan)
			scanDone(repo)
		}(i, spec)
	}
	wg.Wait()
	return results
}

// scanCheckout discovers the skills in dir, a checkout of spec.
func scanCheckout(log *slog.Logger, dir string, spec repoSpec, plan scanPlan) repoResult {
	res := repoResult{Repo: spec}
	log.Info(plan.log + ": discovering skills")
	found, warnings, err := skills.Discover(dir)
	if err != nil {
		log.Warn(plan.log+": discovery failed", "error", err.Error())
		res.Error, res.status = fmt.Sprintf("scan repository: %v", err), http.StatusInternalServerError
		return res
	}
	log.Info(plan.log+": skills found", "skills", len(found), "warnings", len(warnings))
	for _, s := range found {
		view := skillView{Skill: s, Repo: spec}
		if plan.readText {
			view.Text, view.Truncated, err = readSkillText(filepath.Join(dir, filepath.FromSlash(s.Path)))
			if err != nil {
				log.Warn(plan.log+": read SKILL.md failed", "path", s.Path, "error", err.Error())
				res.Error, res.status = fmt.Sprintf("read %s: %v", s.Path, err), http.StatusInternalServerError
				return res
			}
		}
		res.skills = append(res.skills, view)
	}
	for _, w := range warnings {
		res.warnings = append(res.warnings, w.String())
	}
	res.Count = len(res.skills)
	return res
}

// clusterPrompt asks Claude to group the skills. Only ids, names and
// descriptions are sent.
func clusterPrompt(all []clusterSkill) string {
	type item struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	items := make([]item, len(all))
	for i, s := range all {
		items[i] = item{ID: s.ID, Name: s.Name, Description: truncate(s.Description, maxPromptDescription)}
	}
	list, _ := json.MarshalIndent(items, "", "  ")

	return `You are given a list of Claude Code skills (id, name, description) collected from several git repositories.
Group the skills by meaning into clusters of related skills. Choose the number of clusters yourself; similar skills from different repositories belong together.

Respond with ONLY a JSON array, no markdown fences and no other text, in exactly this format:
[{"name": "<short cluster name>", "description": "<one sentence about the cluster>", "skills": ["<id>", ...]}]

Rules:
- use only the ids from the list below;
- put every skill into exactly one cluster.

Skills:
` + string(list) + "\n"
}

// parseClusters validates the model answer. Unknown ids are ignored, a skill
// listed in several clusters stays in the first one, empty clusters are
// dropped and skills the model left out go into an "Other" cluster.
func parseClusters(answer string, all []clusterSkill) ([]skillCluster, error) {
	var raw []struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Skills      []json.RawMessage `json:"skills"`
	}
	if err := json.Unmarshal([]byte(extractJSONArray(answer)), &raw); err != nil {
		return nil, fmt.Errorf("claude returned invalid JSON (%v): %s", err, truncate(strings.TrimSpace(answer), 300))
	}

	byID := make(map[string]clusterSkill, len(all))
	for _, s := range all {
		byID[s.ID] = s
	}
	used := map[string]bool{}
	var clusters []skillCluster
	for _, rc := range raw {
		c := skillCluster{Name: strings.TrimSpace(rc.Name), Description: strings.TrimSpace(rc.Description)}
		if c.Name == "" {
			c.Name = "Unnamed cluster"
		}
		for _, rawID := range rc.Skills {
			var id string
			if json.Unmarshal(rawID, &id) != nil {
				continue
			}
			s, ok := byID[strings.TrimSpace(id)]
			if !ok || used[s.ID] {
				continue
			}
			used[s.ID] = true
			c.Skills = append(c.Skills, s)
		}
		if len(c.Skills) > 0 {
			clusters = append(clusters, c)
		}
	}

	other := skillCluster{Name: otherClusterName, Description: "Skills the model did not assign to any cluster."}
	for _, s := range all {
		if !used[s.ID] {
			other.Skills = append(other.Skills, s)
		}
	}
	if len(other.Skills) > 0 {
		clusters = append(clusters, other)
	}
	return clusters, nil
}

// extractJSONArray returns the outermost [...] of s, tolerating markdown
// fences or prose around the JSON.
func extractJSONArray(s string) string {
	start, end := strings.Index(s, "["), strings.LastIndex(s, "]")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}
