package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func assetFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(gzipWriter)
	files := map[string]assetFile{}
	for _, file := range []struct{ name, text string }{{"script.js", "window.fixture = true;"}, {"LICENSE", "fixture license"}} {
		source := "package/" + file.name
		if err := archive.WriteHeader(&tar.Header{Name: source, Mode: 0600, Size: int64(len(file.text))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(archive, file.text); err != nil {
			t.Fatal(err)
		}
		files[source] = assetFile{Path: file.name, SHA256: digest([]byte(file.text))}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	manifest, err := json.Marshal(assetConfig{Assets: []assetSet{
		{Name: "first", Version: "1.0.0", URL: "https://assets.invalid/first-1.0.0.tgz", SHA256: digest(data), License: "fixture", Files: files},
		{Name: "second", Version: "1.0.0", URL: "https://assets.invalid/second-1.0.0.tgz", SHA256: digest(data), License: "fixture", Files: files},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return manifest, data
}

func fixtureClient(data []byte) *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
}

func TestAssetInstallRecoveryAndOfflineReuse(t *testing.T) {
	manifest, data := assetFixture(t)
	base := t.TempDir()
	target := assetDirectory(base, manifest)
	for _, broken := range [][]byte{data[:len(data)/2], []byte("wrong checksum")} {
		if _, err := installAssets(context.Background(), base, manifest, fixtureClient(broken)); err == nil {
			t.Fatal("accepted broken asset")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("published incomplete assets")
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() != "install.lock" {
				t.Fatalf("left temporary output: %s", entry.Name())
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := installAssets(ctx, base, manifest, fixtureClient(data)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	installed, err := installAssets(context.Background(), base, manifest, fixtureClient(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		license, err := os.ReadFile(filepath.Join(installed, name, "LICENSE"))
		if err != nil || string(license) != "fixture license" {
			t.Fatalf("license: %q %v", license, err)
		}
	}
	offline := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		t.Error("offline reuse contacted network")
		return nil, errors.New("offline")
	})}
	if path, err := installAssets(context.Background(), base, manifest, offline); err != nil || path != installed {
		t.Fatalf("reuse %s %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(installed, "first", "script.js"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installAssets(context.Background(), base, manifest, fixtureClient(data)); err != nil {
		t.Fatal(err)
	}
}

func TestAssetManifestAndFileChecks(t *testing.T) {
	manifest, data := assetFixture(t)
	config, err := readAssetConfig(manifest)
	if err != nil {
		t.Fatal(err)
	}
	file := config.Assets[0].Files["package/script.js"]
	file.SHA256 = digest([]byte("other"))
	config.Assets[0].Files["package/script.js"] = file
	changed, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installAssets(context.Background(), t.TempDir(), changed, fixtureClient(data)); err == nil {
		t.Fatal("accepted invalid file checksum")
	}
	file.Path = "../escape"
	config.Assets[0].Files["package/script.js"] = file
	changed, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readAssetConfig(changed); err == nil {
		t.Fatal("accepted traversal manifest")
	}
}
