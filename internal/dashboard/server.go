// Package dashboard serves the local Intropy integration dashboard: a small
// stdlib net/http server that exposes a read-only JSON API over the
// integrations discovered under a workspace root and serves the embedded SPA
// that renders them. It starts no integration processes — the only thing it
// executes is each system host's `graph` verb, to obtain the declared
// topology the flow view draws.
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// Options configures a dashboard server. The zero value is not usable; Root is
// required and Addr/Port select the listen address.
type Options struct {
	// Root is the workspace directory scanned for integrations.
	Root string
	// Addr is the host/interface to bind, e.g. "127.0.0.1". Empty binds all.
	Addr string
	// Port is the TCP port to bind. 0 asks the OS for a free port.
	Port int
	// OpenBrowser opens the dashboard URL in the default browser on start.
	OpenBrowser bool
	// Version is reported by GET /api/health.
	Version string
	// Stdout and Stderr receive program output and diagnostics; both default
	// to the process streams when nil.
	Stdout io.Writer
	Stderr io.Writer
}

// shutdownTimeout bounds how long in-flight requests have to drain on Ctrl+C.
const shutdownTimeout = 5 * time.Second

// Serve starts the dashboard and blocks until ctx is cancelled (e.g. SIGINT)
// or the server fails. On cancellation it shuts down gracefully and returns
// nil; it returns a non-nil error only for a real startup or runtime failure.
func Serve(ctx context.Context, opts Options) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	root := opts.Root
	if root == "" {
		root = "."
	}

	handler, err := newHandler(root, opts.Version, hostGraphProvider(root))
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	addr := net.JoinHostPort(opts.Addr, fmt.Sprintf("%d", opts.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("run: cannot listen on %s: %w", addr, err)
	}

	url := displayURL(opts.Addr, ln.Addr())
	fmt.Fprintf(opts.Stderr, "Intropy dashboard running at %s\n", url)
	fmt.Fprintln(opts.Stderr, "Press Ctrl+C to stop.")

	if opts.OpenBrowser {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(opts.Stderr, "warning: could not open browser: %v\n", err)
		}
	}

	srv := &http.Server{Handler: handler}
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(opts.Stderr, "\nShutting down…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("run: shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("run: server error: %w", err)
		}
		return nil
	}
}

// displayURL builds a browser-friendly URL. It uses the actual bound port
// (important when Port was 0) and rewrites wildcard bind hosts to localhost.
func displayURL(host string, addr net.Addr) string {
	port := 0
	if tcp, ok := addr.(*net.TCPAddr); ok {
		port = tcp.Port
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
}
