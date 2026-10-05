package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"time"
)

// assetManifest pins the viewer's browser assets (Mermaid, KaTeX, Redoc) by version
// and checksum. They install once under assetBase and are served offline.
//
//go:embed docs-assets.json
var assetManifest []byte

const assetBase = ".local/tools/docs"

type assetFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type assetSet struct {
	Name    string               `json:"name"`
	Version string               `json:"version"`
	URL     string               `json:"url"`
	SHA256  string               `json:"sha256"`
	License string               `json:"license"`
	Files   map[string]assetFile `json:"files"`
}
type assetConfig struct {
	Assets []assetSet `json:"assets"`
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func assetDirectory(base string, manifest []byte) string {
	return filepath.Join(base, "bundle-"+digest(manifest)[:16])
}
func readAssetConfig(manifest []byte) (assetConfig, error) {
	var config assetConfig
	if err := json.Unmarshal(manifest, &config); err != nil {
		return config, err
	}
	if len(config.Assets) == 0 {
		return config, fmt.Errorf("empty viewer asset manifest")
	}
	for _, asset := range config.Assets {
		if !filepath.IsLocal(asset.Name) || asset.Name != filepath.Base(asset.Name) || asset.Version == "" || asset.License == "" || len(asset.Files) == 0 {
			return config, fmt.Errorf("invalid asset set %q", asset.Name)
		}
		for _, file := range asset.Files {
			if !filepath.IsLocal(file.Path) || file.SHA256 == "" {
				return config, fmt.Errorf("invalid asset file %q", file.Path)
			}
		}
	}
	return config, nil
}

func verifyAssets(root string, config assetConfig) error {
	confined, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer confined.Close()
	for _, asset := range config.Assets {
		for _, file := range asset.Files {
			f, err := confined.Open(filepath.Join(asset.Name, file.Path))
			if err != nil {
				return err
			}
			h := sha256.New()
			_, copyErr := io.Copy(h, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if fmt.Sprintf("%x", h.Sum(nil)) != file.SHA256 {
				return fmt.Errorf("asset checksum mismatch: %s/%s", asset.Name, file.Path)
			}
		}
	}
	return nil
}

func setupAssets(ctx context.Context) (string, error) {
	return installAssets(ctx, assetBase, assetManifest, &http.Client{Timeout: 2 * time.Minute})
}

// assetFiles lists the servable "name/path" entries of a manifest.
func assetFiles(config assetConfig) map[string]bool {
	files := map[string]bool{}
	for _, asset := range config.Assets {
		for _, file := range asset.Files {
			files[path.Join(asset.Name, file.Path)] = true
		}
	}
	return files
}

// installAssets publishes one immutable complete bundle. Checks run before any
// network access, so an installed bundle starts offline. All temporary output
// belongs to this worktree and is removed on cancellation or failure.
func installAssets(ctx context.Context, base string, manifest []byte, client *http.Client) (string, error) {
	config, err := readAssetConfig(manifest)
	if err != nil {
		return "", err
	}
	target := assetDirectory(base, manifest)
	if verifyAssets(target, config) == nil {
		return target, nil
	}
	lock, err := acquireLock(filepath.Join(base, "install.lock"))
	if err != nil {
		return "", fmt.Errorf("viewer asset setup lock: %w", err)
	}
	defer lock.Close()
	if verifyAssets(target, config) == nil {
		return target, nil
	}
	stage, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	for _, asset := range config.Assets {
		if err := installAsset(ctx, stage, asset, client); err != nil {
			return "", fmt.Errorf("install %s %s: %w", asset.Name, asset.Version, err)
		}
	}
	if err := verifyAssets(stage, config); err != nil {
		return "", err
	}
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.Rename(stage, target); err != nil {
		return "", err
	}
	return target, nil
}

func installAsset(ctx context.Context, stage string, asset assetSet, client *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", response.Status)
	}
	const maxArchive = 32 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxArchive+1))
	if err != nil {
		return err
	}
	if len(data) > maxArchive || digest(data) != asset.SHA256 {
		return fmt.Errorf("archive checksum mismatch")
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	archive := tar.NewReader(gzipReader)
	found := map[string]bool{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		file, wanted := asset.Files[header.Name]
		if !wanted {
			continue
		}
		if found[header.Name] || header.Typeflag != tar.TypeReg || header.Size > 16<<20 {
			return fmt.Errorf("invalid archive entry %s", header.Name)
		}
		found[header.Name] = true
		data, err := io.ReadAll(archive)
		if err != nil {
			return err
		}
		if digest(data) != file.SHA256 {
			return fmt.Errorf("file checksum mismatch: %s", file.Path)
		}
		target := filepath.Join(stage, asset.Name, file.Path)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return err
		}
	}
	if len(found) != len(asset.Files) {
		return fmt.Errorf("archive is missing required files")
	}
	return nil
}

// acquireLock takes an exclusive, non-blocking flock so two concurrent setups
// in one checkout cannot interleave their staging directories.
func acquireLock(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("another setup is running")
		}
		return nil, err
	}
	return f, nil
}
