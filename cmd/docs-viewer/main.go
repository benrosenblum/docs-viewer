// Command docs-viewer serves a read-only, Obsidian-style viewer for the
// documentation directories of the repository in the working directory, with
// live reload. It is a local development tool:
//
//	docs-viewer [flags] [run|start|stop|status|setup]
//
// run (the default) serves in the foreground, start serves in the
// background and logs to .local/logs/docs.log, stop and status manage that
// server, and setup only installs the pinned browser assets.
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
	"strings"
	"syscall"
	"time"

	"github.com/benrosenblum/docs-viewer/docsview"
)

// viewer is this checkout's server state, next to the pinned assets.
var viewer = instance{dir: ".local/run/docs", log: ".local/logs/docs.log"}

// options are the settings from the flags.
type options struct {
	addr, specVault, theme string
	vaults                 []string
}

func main() {
	var o options
	flag.StringVar(&o.addr, "addr", "127.0.0.1:7000", "listen address; port 0 selects a free port")
	vaults := flag.String("vaults", "docs", "documentation directories, separated by commas; the first holds the home note")
	flag.StringVar(&o.specVault, "spec-vault", "", "the directory of -vaults that holds OpenAPI specs")
	flag.StringVar(&o.theme, "theme", "", "theme stylesheet; the default is the built-in theme")
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: docs-viewer [flags] [run|start|stop|status|setup]")
		flag.PrintDefaults()
	}
	flag.Parse()
	o.vaults = strings.Split(*vaults, ",")
	command := "run"
	switch flag.NArg() {
	case 0:
	case 1:
		command = flag.Arg(0)
	default:
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := dispatch(ctx, command, o); err != nil {
		slog.Error("docs viewer failed", "command", command, "err", err)
		os.Exit(1)
	}
}

func dispatch(ctx context.Context, command string, o options) error {
	// The server state is below the working directory.
	for _, vault := range o.vaults {
		if info, err := os.Stat(vault); err != nil || !info.IsDir() {
			return fmt.Errorf("no documentation directory %q; run from the repository root", vault)
		}
	}
	switch command {
	case "run":
		return run(ctx, o)
	case "start":
		// Install in the foreground so download errors reach the terminal.
		if _, err := setupAssets(ctx); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		flags := os.Args[1 : len(os.Args)-flag.NArg()]
		return viewer.start(ctx, append(append([]string{exe}, flags...), "run"))
	case "stop":
		return viewer.stop()
	case "status":
		return viewer.status()
	case "setup":
		assets, err := setupAssets(ctx)
		if err == nil {
			fmt.Println("docs viewer assets verified:", assets)
		}
		return err
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func run(ctx context.Context, o options) error {
	release, err := viewer.hold()
	if err != nil {
		return err
	}
	defer release()
	assets, err := setupAssets(ctx)
	if err != nil {
		return err
	}
	config, err := readAssetConfig(assetManifest)
	if err != nil {
		return err
	}
	handler, err := docsview.New(docsview.Config{
		Root:         ".",
		Vaults:       o.vaults,
		SpecVault:    o.specVault,
		Assets:       assets,
		AssetFiles:   assetFiles(config),
		AssetVersion: strings.TrimPrefix(filepath.Base(assets), "bundle-"),
		Theme:        o.theme,
	})
	if err != nil {
		return err
	}
	defer handler.Close()
	if err := handler.GitError(); err != nil {
		slog.Warn("docs viewer diff features are off", "reason", err)
	}
	return serve(ctx, o.addr, handler, viewer.publish)
}

// serve listens on addr, passes the URL to ready, and serves until ctx ends.
// Request contexts are cancelled before shutdown so the live-reload event
// streams cannot hold the server open.
func serve(ctx context.Context, addr string, handler http.Handler, ready func(url string) error) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w", addr, err)
	}
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
		BaseContext:       func(net.Listener) context.Context { return requests },
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	url := "http://" + listener.Addr().String() + "/"
	if err := ready(url); err != nil {
		_ = server.Close()
		<-served
		return err
	}
	fmt.Println("docs viewer:", url)

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	cancelRequests()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = server.Shutdown(shutdown)
	if err != nil {
		_ = server.Close()
	}
	if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
		err = errors.Join(err, serveErr)
	}
	return err
}
