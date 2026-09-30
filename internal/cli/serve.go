package cli

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
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
	addr string
}

func newServeCommand() *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a web UI for listing skills in a git repository",
		Long: `Starts a local web server. Open it in a browser, enter a repository URL
(and optionally a branch or tag) to see the skills it contains.`,
		Example: `  atlas serve
  atlas serve --addr 127.0.0.1:9000`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.addr, "addr", "localhost:8080", "listen address (host:port)")
	return cmd
}

func runServe(cmd *cobra.Command, opts serveOptions) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", opts.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.addr, err)
	}
	srv := &http.Server{
		Handler:           newServeHandler(checkout),
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
	Searched bool
	Error    string
	Skills   []skillView
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
func newServeHandler(checkout checkoutFunc) http.Handler {
	var recent history
	mux := http.NewServeMux()
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
			Repo: strings.TrimSpace(r.URL.Query().Get("repo")),
			Ref:  strings.TrimSpace(r.URL.Query().Get("ref")),
		}
		status := http.StatusOK
		if data.Repo != "" {
			data.Searched = true
			status = scan(r.Context(), checkout, &data)
			if data.Error == "" {
				recent.add(historyEntry{Repo: data.Repo, Ref: data.Ref, Count: len(data.Skills)})
			}
		}
		data.History = recent.list()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		// Headers are already sent, so a template error can't be reported.
		_ = servePage.Execute(w, data)
	})
	return mux
}

// scan fills data with the skills of data.Repo and returns the HTTP status.
func scan(ctx context.Context, checkout checkoutFunc, data *pageData) int {
	if err := validateRemote(data.Repo); err != nil {
		data.Error = err.Error()
		return http.StatusBadRequest
	}

	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	dir, cleanup, err := checkout(ctx, data.Repo, data.Ref)
	defer cleanup()
	if err != nil {
		data.Error = err.Error()
		return http.StatusBadGateway
	}

	found, warnings, err := skills.Discover(dir)
	if err != nil {
		data.Error = fmt.Sprintf("scan repository: %v", err)
		return http.StatusInternalServerError
	}
	for _, s := range found {
		view := skillView{Skill: s}
		view.Text, view.Truncated, err = readSkillText(filepath.Join(dir, filepath.FromSlash(s.Path)))
		if err != nil {
			data.Error = fmt.Sprintf("read %s: %v", s.Path, err)
			return http.StatusInternalServerError
		}
		data.Skills = append(data.Skills, view)
	}
	for _, w := range warnings {
		data.Warnings = append(data.Warnings, w.String())
	}
	return http.StatusOK
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
