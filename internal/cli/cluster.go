package cli

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/example/atlas/internal/skills"
)

//go:embed cluster.html
var clusterPageHTML string

var clusterPage = template.Must(template.New("cluster").Parse(clusterPageHTML))

// maxClusterRepos is how many repositories a single /cluster request may scan.
const maxClusterRepos = 10

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

// repoSpec is one line of the /cluster form.
type repoSpec struct {
	URL string
	Ref string
}

func (r repoSpec) String() string {
	if r.Ref == "" {
		return r.URL
	}
	return r.URL + " " + r.Ref
}

// parseRepoList parses "<url>" or "<url> <ref>" lines, skipping blank lines
// and dropping duplicates. Malformed lines are returned as per-repo errors.
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
			bad = append(bad, repoResult{Repo: spec, Error: fmt.Sprintf("invalid line %q: use \"<url>\" or \"<url> <ref>\"", strings.TrimSpace(line))})
			continue
		}
		if err := validateRemote(spec.URL); err != nil {
			bad = append(bad, repoResult{Repo: spec, Error: err.Error()})
			continue
		}
		specs = append(specs, spec)
	}
	return specs, bad
}

// repoResult is the outcome of scanning one repository.
type repoResult struct {
	Repo   repoSpec
	Count  int
	Error  string
	skills []skills.Skill
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
	Repos     string
	Submitted bool
	Error     string
	Results   []repoResult
	Total     int
	Clusters  []skillCluster
}

func (d clusterPageData) RepoErrors() []repoResult {
	var out []repoResult
	for _, r := range d.Results {
		if r.Error != "" {
			out = append(out, r)
		}
	}
	return out
}

// serveCluster handles GET /cluster?repos=<one repo per line>.
func serveCluster(checkout checkoutFunc, cfg serveConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data := clusterPageData{Repos: strings.TrimSpace(r.URL.Query().Get("repos"))}
		status := http.StatusOK
		if data.Repos != "" {
			data.Submitted = true
			status = clusterRepos(r.Context(), checkout, cfg, &data)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		// Headers are already sent, so a template error can't be reported.
		_ = clusterPage.Execute(w, data)
	}
}

// clusterRepos scans the repositories in data.Repos, clusters their skills
// with Claude and returns the HTTP status.
func clusterRepos(ctx context.Context, checkout checkoutFunc, cfg serveConfig, data *clusterPageData) int {
	specs, bad := parseRepoList(data.Repos)
	if n := len(specs) + len(bad); n > maxClusterRepos {
		data.Error = fmt.Sprintf("too many repositories (%d): at most %d are allowed", n, maxClusterRepos)
		return http.StatusBadRequest
	}

	data.Results = append(scanRepos(ctx, checkout, specs), bad...)
	var all []clusterSkill
	for _, res := range data.Results {
		for _, s := range res.skills {
			all = append(all, clusterSkill{Skill: s, ID: fmt.Sprintf("s%d", len(all)+1), Repo: res.Repo})
		}
	}
	data.Total = len(all)
	if len(specs) == 0 {
		data.Error = "no valid repositories to scan"
		return http.StatusBadRequest
	}
	if len(all) == 0 {
		return http.StatusOK
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.claudeTimeout)
	defer cancel()
	answer, err := cfg.claude(ctx, clusterPrompt(all))
	if err != nil {
		data.Error = err.Error()
		return http.StatusBadGateway
	}
	data.Clusters, err = parseClusters(answer, all)
	if err != nil {
		data.Error = err.Error()
		return http.StatusBadGateway
	}
	return http.StatusOK
}

// scanRepos clones and scans the repositories in parallel, keeping their order.
func scanRepos(ctx context.Context, checkout checkoutFunc, specs []repoSpec) []repoResult {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	results := make([]repoResult, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec repoSpec) {
			defer wg.Done()
			res := repoResult{Repo: spec}
			dir, cleanup, err := checkout(ctx, spec.URL, spec.Ref)
			defer cleanup()
			if err != nil {
				res.Error = err.Error()
			} else if found, _, err := skills.Discover(dir); err != nil {
				res.Error = fmt.Sprintf("scan repository: %v", err)
			} else {
				res.skills, res.Count = found, len(found)
			}
			results[i] = res
		}(i, spec)
	}
	wg.Wait()
	return results
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
