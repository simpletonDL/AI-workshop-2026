package cli

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed progress.js
var progressJS string

// progressHeader carries the id under which a page request reports its
// progress. The page script generates the id, sends it with the request and
// polls GET /progress?id=<id> until the page arrives.
const progressHeader = "X-Atlas-Progress"

// progressRetention is how long a finished job stays visible at /progress,
// so the last poll can still see 100%.
const progressRetention = 30 * time.Second

// maxProgressJobs bounds how many jobs are tracked at once. Requests beyond
// the limit still work, they just don't report progress.
const maxProgressJobs = 100

// progressID is the accepted format of a client-generated job id.
var progressID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// progress tracks the stage and completion of one long-running request. Work
// is measured in weighted steps: percent is done/total, stays below 100 until
// finish is called and never goes down.
type progress struct {
	mu       sync.Mutex
	stage    string
	done     int
	total    int
	finished bool
}

// progressSnapshot is the JSON returned by GET /progress.
type progressSnapshot struct {
	Stage   string `json:"stage"`
	Percent int    `json:"percent"`
	Done    bool   `json:"done"`
}

// setTotal sets the number of steps of the whole job.
func (p *progress) setTotal(total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total = total
}

// setStage sets the human-readable description of the current stage.
func (p *progress) setStage(stage string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stage = stage
}

// advance marks n more steps as done and, if stage is not empty, moves to it.
func (p *progress) advance(n int, stage string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n > 0 {
		p.done = min(p.done+n, p.total)
	}
	if stage != "" {
		p.stage = stage
	}
}

// part is a piece of a job worth steps that completes gradually, e.g. a clone.
type part struct {
	p     *progress
	steps int
	done  int // guarded by p.mu
}

// part starts a piece of work worth steps (not yet counted as done).
func (p *progress) part(steps int) *part {
	return &part{p: p, steps: steps}
}

// set moves the part to percent (0–100) of its steps; it never goes back.
func (pt *part) set(percent int) {
	pt.p.mu.Lock()
	defer pt.p.mu.Unlock()
	pt.moveTo(pt.steps * min(max(percent, 0), 100) / 100)
}

// finish marks the whole part as done and, if stage is not empty, moves to it.
func (pt *part) finish(stage string) {
	pt.p.mu.Lock()
	defer pt.p.mu.Unlock()
	pt.moveTo(pt.steps)
	if stage != "" {
		pt.p.stage = stage
	}
}

func (pt *part) moveTo(n int) {
	if n > pt.done {
		pt.p.done = min(pt.p.done+n-pt.done, pt.p.total)
		pt.done = n
	}
}

// finish marks the job as complete: percent becomes 100.
func (p *progress) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done, p.finished, p.stage = p.total, true, "Done"
}

func (p *progress) snapshot() progressSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := progressSnapshot{Stage: p.stage, Done: p.finished}
	switch {
	case p.finished:
		s.Percent = 100
	case p.total > 0:
		s.Percent = min(p.done*100/p.total, 99)
	}
	return s
}

// progressJobs holds the jobs that report progress, by client-generated id.
type progressJobs struct {
	mu   sync.Mutex
	jobs map[string]*progress
}

// start returns the tracker of a new job. If id is invalid, already used or
// too many jobs are running, the tracker works but is not visible at
// /progress.
func (j *progressJobs) start(id string) (p *progress, done func()) {
	p = &progress{}
	if !progressID.MatchString(id) {
		return p, p.finish
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.jobs == nil {
		j.jobs = map[string]*progress{}
	}
	if _, used := j.jobs[id]; used || len(j.jobs) >= maxProgressJobs {
		return p, p.finish
	}
	j.jobs[id] = p
	return p, func() {
		p.finish()
		time.AfterFunc(progressRetention, func() { j.remove(id) })
	}
}

func (j *progressJobs) remove(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.jobs, id)
}

func (j *progressJobs) get(id string) (*progress, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	p, ok := j.jobs[id]
	return p, ok
}

// serveProgress handles GET /progress?id=<id>.
func (j *progressJobs) serveProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	p, ok := j.get(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "unknown progress id", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p.snapshot())
}

func serveProgressJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(progressJS))
}

// loggerKey is the context key of the request-scoped logger.
type loggerKey struct{}

// logFrom returns the request-scoped logger stored by logRequests.
func logFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// statusRecorder remembers the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// logRequests logs the start and end of every request with its status and
// duration. Only the method and path are logged: queries may contain
// repository URLs with credentials. Progress polls are logged at debug level.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	var seq atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		level := slog.LevelInfo
		if r.URL.Path == "/progress" || r.URL.Path == "/progress.js" || r.URL.Path == "/repos.js" || r.URL.Path == "/dancer.js" || r.URL.Path == "/stars.js" {
			level = slog.LevelDebug
		}
		reqLog := log.With("req", seq.Add(1))
		start := time.Now()
		reqLog.Log(r.Context(), level, "request started", "method", r.Method, "path", r.URL.Path)
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), loggerKey{}, reqLog)))
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		reqLog.Log(r.Context(), level, "request finished", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
	})
}

// redactRepo removes credentials (user info) from a repository URL so it can
// be logged or shown as a progress stage. scp-like remotes (git@host:path)
// are kept as is: their user is not a secret.
func redactRepo(repo string) string {
	if !strings.Contains(repo, "://") {
		return repo
	}
	u, err := url.Parse(repo)
	if err != nil {
		// Can't tell where the credentials are; drop everything before '@'.
		if i := strings.LastIndex(repo, "@"); i >= 0 {
			return "***" + repo[i:]
		}
		return repo
	}
	if u.User == nil {
		return repo
	}
	u.User = nil
	return u.String()
}

// redactError replaces the raw repository URL in an error message with its
// redacted form.
func redactError(err error, repo string) string {
	return strings.ReplaceAll(err.Error(), repo, redactRepo(repo))
}
