package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestMain doubles as the background server when a test starts this binary
// again with DOCS_TEST_CHILD set.
func TestMain(m *testing.M) {
	switch os.Getenv("DOCS_TEST_CHILD") {
	case "serve":
		os.Exit(childServe(instance{dir: os.Getenv("DOCS_TEST_DIR")}))
	case "fail":
		fmt.Println("boom: cannot serve")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

// childServe is run without the docs handler: the lock, publish, and
// shutdown paths are the real ones.
func childServe(in instance) int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	release, err := in.hold()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer release()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	if err := serve(ctx, "127.0.0.1:0", ok, in.publish); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func testInstance(t *testing.T, child string) instance {
	t.Helper()
	dir := t.TempDir()
	in := instance{dir: filepath.Join(dir, "run"), log: filepath.Join(dir, "logs", "docs.log")}
	t.Setenv("DOCS_TEST_CHILD", child)
	t.Setenv("DOCS_TEST_DIR", in.dir)
	t.Cleanup(func() { _ = in.stop() })
	return in
}

func TestBackgroundLifecycle(t *testing.T) {
	in := testInstance(t, "serve")
	if err := in.start(context.Background(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
	pid, err := in.holder()
	if err != nil || pid == 0 {
		t.Fatalf("holder %d %v", pid, err)
	}
	s, ok := in.read()
	if !ok || s.PID != pid {
		t.Fatalf("state %+v, holder %d", s, pid)
	}
	resp, err := http.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body %q", body)
	}

	// A second start reuses the server, and a foreground server cannot bind.
	if err := in.start(context.Background(), []string{os.Args[0]}); err != nil {
		t.Fatal(err)
	}
	if again, _ := in.holder(); again != pid {
		t.Fatalf("second start replaced pid %d with %d", pid, again)
	}
	if _, err := in.hold(); err == nil || !strings.Contains(err.Error(), s.URL) {
		t.Fatalf("hold while running: %v", err)
	}

	if err := in.stop(); err != nil {
		t.Fatal(err)
	}
	if now, _ := in.holder(); now != 0 {
		t.Fatalf("pid %d still holds the lock", now)
	}
	if _, ok := in.read(); ok {
		t.Fatal("state survived shutdown")
	}
	if err := in.stop(); err != nil {
		t.Fatalf("stop when stopped: %v", err)
	}
}

func TestBackgroundStartReportsEarlyExit(t *testing.T) {
	in := testInstance(t, "fail")
	err := in.start(context.Background(), []string{os.Args[0]})
	if err == nil || !strings.Contains(err.Error(), "boom: cannot serve") {
		t.Fatalf("start: %v", err)
	}
	if pid, _ := in.holder(); pid != 0 {
		t.Fatalf("pid %d holds the lock", pid)
	}
}
