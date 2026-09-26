// Command dirserve serves one directory over HTTP: files at their own clean
// URLs for curl, and the same URLs as a read-only web UI for a human.
//
//	dirserve [--root DIR] [--addr HOST:PORT] [--hidden] [--token T]
//	         [--max-preview-bytes N] [--quiet] [--version]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"dirserve/internal/fsx"
	"dirserve/internal/server"
)

const (
	defaultAddr         = "127.0.0.1:8080"
	defaultMaxPreview   = 1 << 20 // 1 MiB
	shutdownGracePeriod = 5 * time.Second
	containerDataDir    = "/data"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dirserve: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("dirserve", flag.ContinueOnError)
	root := fs.String("root", "", "directory to serve (default: $DIRSERVE_ROOT, /data if present, else .)")
	addr := fs.String("addr", "", "listen address (default: $DIRSERVE_ADDR, else "+defaultAddr+")")
	hidden := fs.Bool("hidden", false, "hide dotfiles from listings and refuse to serve them")
	token := fs.String("token", "", "require 'Authorization: Bearer <token>' on every request")
	maxPreview := fs.Int64("max-preview-bytes", -1, "largest file previewed inline in the UI (default 1 MiB)")
	quiet := fs.Bool("quiet", false, "do not log requests")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "dirserve serves one directory read-only over HTTP.\n\nUsage:\n  dirserve [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprint(fs.Output(), "\nEnvironment:\n  DIRSERVE_ROOT, DIRSERVE_ADDR, DIRSERVE_TOKEN,\n  DIRSERVE_MAX_PREVIEW_BYTES, DIRSERVE_HIDDEN\n")
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Println("dirserve " + server.Version())
		return nil
	}

	// Flags win over env, env over defaults (§9).
	resolvedAddr := firstNonEmpty(*addr, os.Getenv("DIRSERVE_ADDR"), defaultAddr)
	resolvedRoot := firstNonEmpty(*root, os.Getenv("DIRSERVE_ROOT"), defaultRoot())
	resolvedToken := firstNonEmpty(*token, os.Getenv("DIRSERVE_TOKEN"))
	hideDots := *hidden || envBool("DIRSERVE_HIDDEN")
	preview := *maxPreview
	if preview < 0 {
		preview = envInt("DIRSERVE_MAX_PREVIEW_BYTES", defaultMaxPreview)
	}

	// Fail fast: a root that is missing or is not a directory is an error at
	// startup, never an empty server that answers 200 to everything (§9).
	absRoot, err := filepath.Abs(resolvedRoot)
	if err != nil {
		return fmt.Errorf("resolving root: %w", err)
	}
	if info, err := os.Stat(absRoot); err != nil {
		return fmt.Errorf("root %s: %w", absRoot, err)
	} else if !info.IsDir() {
		return fmt.Errorf("root %s: not a directory", absRoot)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	fsys, err := fsx.Open(absRoot, hideDots)
	if err != nil {
		return err
	}
	defer fsys.Close()

	ln, err := net.Listen("tcp", resolvedAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", resolvedAddr, err)
	}

	srv := &http.Server{
		Handler: server.New(server.Config{
			FS:              fsys,
			Token:           resolvedToken,
			MaxPreviewBytes: preview,
			Quiet:           *quiet,
			Logger:          logger,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	fmt.Fprintf(os.Stderr, "dirserve serving %s at http://%s\n", absRoot, ln.Addr())
	if !server.IsLoopback(ln.Addr().String()) && resolvedToken == "" {
		fmt.Fprintf(os.Stderr,
			"WARNING: bound to %s with no --token: anyone who can reach this port can read the whole tree. Set --token or bind to 127.0.0.1.\n",
			ln.Addr())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	stop() // a second signal now kills the process outright
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	<-serveErr
	return nil
}

// defaultRoot implements the §9 resolution rule: an explicit --root wins, then
// DIRSERVE_ROOT, then /data when a volume created it (so a container needs no
// flags at all), and finally the working directory.
func defaultRoot() string {
	if st, err := os.Stat(containerDataDir); err == nil && st.IsDir() {
		return containerDataDir
	}
	return "."
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func envBool(name string) bool {
	switch os.Getenv(name) {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}

func envInt(name string, def int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}
