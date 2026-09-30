package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProgressPercent(t *testing.T) {
	var p progress
	if s := p.snapshot(); s.Percent != 0 || s.Done {
		t.Fatalf("empty progress = %+v", s)
	}
	p.setTotal(10)
	p.setStage("Cloning…")
	steps := []struct {
		n       int
		stage   string
		percent int
		want    string
	}{
		{0, "", 0, "Cloning…"},
		{8, "Discovering skills…", 80, "Discovering skills…"},
		{1, "", 90, "Discovering skills…"},
		// All steps done: stays below 100 until finish.
		{1, "Reading…", 99, "Reading…"},
		// Extra steps are clamped.
		{5, "", 99, "Reading…"},
		{-3, "", 99, "Reading…"},
	}
	for i, step := range steps {
		p.advance(step.n, step.stage)
		s := p.snapshot()
		if s.Percent != step.percent || s.Stage != step.want || s.Done {
			t.Errorf("step %d: snapshot = %+v, want %d%% %q", i, s, step.percent, step.want)
		}
	}
	p.finish()
	if s := p.snapshot(); s.Percent != 100 || !s.Done || s.Stage != "Done" {
		t.Errorf("finished snapshot = %+v", s)
	}
}

func TestProgressFinishWithoutSteps(t *testing.T) {
	var p progress
	p.finish()
	if s := p.snapshot(); s.Percent != 100 || !s.Done {
		t.Errorf("snapshot = %+v, want 100%% done", s)
	}
}

const testProgressID = "0123456789abcdef0123456789abcdef"

func TestProgressJobs(t *testing.T) {
	var jobs progressJobs
	for _, id := range []string{"", "short", "has spaces in it, 1234567", strings.Repeat("a", 65), "../../../etc/passwd/xxxx"} {
		p, done := jobs.start(id)
		if _, ok := jobs.get(id); ok {
			t.Errorf("invalid id %q registered", id)
		}
		done()
		if !p.snapshot().Done {
			t.Errorf("done() did not finish the tracker of %q", id)
		}
	}

	p, done := jobs.start(testProgressID)
	if got, ok := jobs.get(testProgressID); !ok || got != p {
		t.Fatal("valid id not registered")
	}
	if dup, _ := jobs.start(testProgressID); dup == p {
		t.Error("duplicate id reused a running job")
	}
	if got, _ := jobs.get(testProgressID); got != p {
		t.Error("duplicate id replaced the running job")
	}
	done()
	if got, ok := jobs.get(testProgressID); !ok || !got.snapshot().Done {
		t.Error("finished job must stay visible for the last poll")
	}
	jobs.remove(testProgressID)
	if _, ok := jobs.get(testProgressID); ok {
		t.Error("job not removed")
	}
}

func TestProgressJobsLimit(t *testing.T) {
	var jobs progressJobs
	for i := 0; i < maxProgressJobs; i++ {
		jobs.start(fmt.Sprintf("job-%016d", i))
	}
	id := "one-too-many-0000000"
	jobs.start(id)
	if _, ok := jobs.get(id); ok {
		t.Errorf("more than %d jobs tracked", maxProgressJobs)
	}
}

func getProgress(t *testing.T, h http.Handler, id string) (int, progressSnapshot) {
	t.Helper()
	code, body := get(t, h, "/progress?id="+id)
	var s progressSnapshot
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatalf("invalid progress JSON %q: %v", body, err)
		}
	}
	return code, s
}

// getWithProgress requests target with a progress id, like the page script.
func getWithProgress(h http.Handler, target, id string) (int, string) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set(progressHeader, id)
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestProgressEndpointUnknownID(t *testing.T) {
	h := newServeHandler((&stubCheckout{}).checkout)
	if code, _ := getProgress(t, h, testProgressID); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/progress?id="+testProgressID, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}

// blockingCheckout waits for release before serving dir.
type blockingCheckout struct {
	dir     string
	started chan struct{}
	release chan struct{}
}

func (b *blockingCheckout) checkout(ctx context.Context, _, _ string) (string, func(), error) {
	b.started <- struct{}{}
	<-b.release
	return b.dir, func() {}, nil
}

func TestServeReportsScanProgress(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/deploy", "---\nname: deploy\ndescription: Deploy\n---\n")
	co := &blockingCheckout{dir: root, started: make(chan struct{}), release: make(chan struct{})}
	h := newServeHandler(co.checkout)

	var wg sync.WaitGroup
	wg.Add(1)
	var code int
	var body string
	go func() {
		defer wg.Done()
		code, body = getWithProgress(h, query("https://user:secret@github.com/org/repo", ""), testProgressID)
	}()

	<-co.started
	status, s := getProgress(t, h, testProgressID)
	if status != http.StatusOK {
		t.Fatalf("progress status = %d", status)
	}
	if s.Stage != "Cloning https://github.com/org/repo…" || s.Percent != 0 || s.Done {
		t.Errorf("progress while cloning = %+v", s)
	}
	close(co.release)
	wg.Wait()

	if code != http.StatusOK || !strings.Contains(body, "1 skill found") {
		t.Fatalf("page status = %d, body:\n%s", code, body)
	}
	if _, s := getProgress(t, h, testProgressID); s.Percent != 100 || !s.Done {
		t.Errorf("progress after the page = %+v, want 100%% done", s)
	}
}

func TestServeProgressOnError(t *testing.T) {
	stub := &stubCheckout{err: errors.New("failed to clone: nope")}
	h := newServeHandler(stub.checkout)
	if code, _ := getWithProgress(h, query("https://github.com/org/repo", ""), testProgressID); code != http.StatusBadGateway {
		t.Fatalf("status = %d", code)
	}
	if _, s := getProgress(t, h, testProgressID); s.Percent != 100 || !s.Done {
		t.Errorf("progress after a failed scan = %+v", s)
	}
}

func TestServeWithoutProgressHeader(t *testing.T) {
	root := t.TempDir()
	h := newServeHandler((&stubCheckout{dir: root}).checkout)
	if code, _ := get(t, h, query("https://github.com/org/repo", "")); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
}

func TestClusterReportsProgress(t *testing.T) {
	co := twoRepos(t)
	var h http.Handler
	var during progressSnapshot
	claude := func(ctx context.Context, prompt string) (string, error) {
		_, during = getProgress(t, h, testProgressID)
		return `[{"name": "Ops", "skills": ["s1", "s2", "s3"]}]`, nil
	}
	h = newServeHandler(co.checkout, withClaude(claude, time.Minute))

	code, body := getWithProgress(h, clusterQuery("https://github.com/org/a", "https://github.com/org/b"), testProgressID)
	if code != http.StatusOK || !strings.Contains(body, "Ops") {
		t.Fatalf("status = %d, body:\n%s", code, body)
	}
	// Two repositories: 2 steps each for cloning and discovery, the same
	// weight again for Claude.
	if during.Stage != "Clustering 3 skills with Claude…" || during.Percent != 50 {
		t.Errorf("progress while calling Claude = %+v", during)
	}
	if _, s := getProgress(t, h, testProgressID); s.Percent != 100 || !s.Done {
		t.Errorf("progress after the page = %+v", s)
	}
}

func TestScanReposProgressStages(t *testing.T) {
	co := twoRepos(t)
	co.errs = map[string]error{"https://github.com/org/b": errors.New("failed to clone: nope")}
	var p progress
	p.setTotal(2 * clusterStepsPerRepo * 2)
	scanRepos(context.Background(), co.checkout, []repoSpec{{URL: "https://github.com/org/a"}, {URL: "https://github.com/org/b"}}, &p)
	// A failed clone still counts its steps, so the scan half is complete.
	s := p.snapshot()
	if s.Percent != 50 || !strings.HasPrefix(s.Stage, "Scanned ") || !strings.Contains(s.Stage, "(2/2)") {
		t.Errorf("progress after scanning = %+v", s)
	}
}

func TestServeProgressScript(t *testing.T) {
	h := newServeHandler((&stubCheckout{}).checkout)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/progress.js", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("status = %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), progressHeader) || !strings.Contains(rec.Body.String(), "/progress?id=") {
		t.Error("script does not use the progress header and endpoint")
	}
	for _, page := range []string{"/", "/cluster"} {
		if _, body := get(t, h, page); !strings.Contains(body, `<script src="/progress.js"></script>`) {
			t.Errorf("%s does not include the progress script", page)
		}
	}
}

func TestServeLogs(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/deploy", "---\nname: deploy\ndescription: Deploy\n---\nSECRET BODY\n")
	var buf bytes.Buffer
	h := newServeHandler((&stubCheckout{dir: root}).checkout, withLogger(slog.New(slog.NewTextHandler(&buf, nil))))

	get(t, h, query("https://user:tok3n@github.com/org/repo", "v1")+"&filter=dep")
	log := buf.String()
	for _, want := range []string{
		`msg="request started"`, "method=GET path=/ ",
		`msg="request finished"`, "status=200", "duration=",
		`msg="scan: cloning" req=1 repo=https://github.com/org/repo ref=v1`,
		`msg="scan: clone done"`,
		`msg="scan: discovering skills"`,
		`msg="scan: skills found"`, "skills=1",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log has no %q:\n%s", want, log)
		}
	}
	for _, leak := range []string{"tok3n", "SECRET BODY", "filter=dep"} {
		if strings.Contains(log, leak) {
			t.Errorf("log leaks %q:\n%s", leak, log)
		}
	}
}

func TestServeLogsCloneFailure(t *testing.T) {
	repo := "https://user:tok3n@github.com/org/repo"
	var buf bytes.Buffer
	stub := &stubCheckout{err: fmt.Errorf("failed to clone %s: not found", repo)}
	h := newServeHandler(stub.checkout, withLogger(slog.New(slog.NewTextHandler(&buf, nil))))
	get(t, h, query(repo, ""))
	log := buf.String()
	if !strings.Contains(log, `msg="scan: clone failed"`) || !strings.Contains(log, "status=502") {
		t.Errorf("clone failure not logged:\n%s", log)
	}
	if strings.Contains(log, "tok3n") {
		t.Errorf("log leaks credentials:\n%s", log)
	}
}

func TestClusterLogs(t *testing.T) {
	co := twoRepos(t)
	fc := &fakeClaude{answer: `[{"name": "Ops", "skills": ["s1", "s2", "s3"]}]`}
	var buf bytes.Buffer
	h := newServeHandler(co.checkout, withClaude(fc.run, time.Minute), withLogger(slog.New(slog.NewTextHandler(&buf, nil))))
	get(t, h, clusterQuery("https://github.com/org/a", "https://github.com/org/b"))
	log := buf.String()
	for _, want := range []string{
		`msg="request started"`, "path=/cluster",
		`msg="cluster: cloning" req=1 repo=https://github.com/org/a`,
		`msg="cluster: clone done" req=1 repo=https://github.com/org/b`,
		`msg="cluster: skills found" req=1 skills=3`,
		`msg="cluster: calling Claude" req=1 skills=3`,
		`msg="cluster: Claude done"`, "clusters=1",
		"status=200",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log has no %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "SECRET BODY") {
		t.Errorf("log leaks SKILL.md contents:\n%s", log)
	}

	buf.Reset()
	fc.err, fc.answer = errors.New("claude failed: boom"), ""
	get(t, h, clusterQuery("https://github.com/org/a"))
	if !strings.Contains(buf.String(), `msg="cluster: Claude failed"`) || !strings.Contains(buf.String(), "status=502") {
		t.Errorf("Claude failure not logged:\n%s", buf.String())
	}
}

func TestProgressPollsLoggedAtDebug(t *testing.T) {
	var buf bytes.Buffer
	h := newServeHandler((&stubCheckout{}).checkout, withLogger(slog.New(slog.NewTextHandler(&buf, nil))))
	get(t, h, "/progress?id="+testProgressID)
	if buf.Len() != 0 {
		t.Errorf("progress poll logged at info level:\n%s", buf.String())
	}
}

func TestRedactRepo(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/org/repo":            "https://github.com/org/repo",
		"https://tok3n@github.com/org/repo":      "https://github.com/org/repo",
		"https://user:tok3n@github.com/org/repo": "https://github.com/org/repo",
		"ssh://git@github.com/org/repo.git":      "ssh://github.com/org/repo.git",
		"git@github.com:org/repo.git":            "git@github.com:org/repo.git",
		"https://user:tok3n@github.com/%zz/repo": "***@github.com/%zz/repo",
	} {
		if got := redactRepo(in); got != want {
			t.Errorf("redactRepo(%q) = %q, want %q", in, got, want)
		}
	}
}
