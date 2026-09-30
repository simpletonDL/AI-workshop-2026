package cli

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os/signal"
	"regexp"
	"strings"
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
	Skills   []skills.Skill
	Warnings []string
}

// newServeHandler returns the web UI handler. GET / shows the form; with a
// ?repo= query it also clones the repository and shows its skills.
func newServeHandler(checkout checkoutFunc) http.Handler {
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
		}

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
	data.Skills = found
	for _, w := range warnings {
		data.Warnings = append(data.Warnings, w.String())
	}
	return http.StatusOK
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
