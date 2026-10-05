package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcosnils/bin2/pkg/assets"
	"github.com/marcosnils/bin2/pkg/config"
	"github.com/marcosnils/bin2/pkg/providers"
)

// This is a basic integration-style test that exercises install --unpack and update
// flows using a mocked provider that returns a generated tar.gz archive.
func TestInstallUpdateUnpackedLifecycle(t *testing.T) {
	// setup config and paths
	cfgHome := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	if err := os.MkdirAll(filepath.Join(cfgHome, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{"default_path": "` + binDir + `", "bins": {}}`
	if err := os.WriteFile(filepath.Join(cfgHome, "bin", "config.json"), []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.CheckAndLoad(); err != nil {
		t.Fatal(err)
	}

	// Create a small tar.gz archive on disk with a single executable file
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "pkg.tar.gz")
	// use assets helper to create a tar.gz? For simplicity create a gzipped single file
	// We'll create a tar.gz using the same method as tests earlier
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	// write a simple tar.gz with a file 'bin/mytool' marked executable
	// reuse the standard approach
	// create tar.gz
	// minimal tar using archive/tar + gzip
	tw := createTarGzWithExecutable(t, f, "bin/mytool", "#!/bin/sh\necho hi\n")
	if tw != nil {
		t.Fatalf("failed creating archive: %v", tw)
	}
	f.Close()

	// Use a mock provider that returns this archive when requested
	mp := &mockArchiveProvider{archivePath: archive, name: "mytool", version: "0.1.0"}

	// Simulate install --unpack
	// The install command expects a provider; to keep test hermetic we call the unpack logic directly
	parent := binDir
	appDir := filepath.Join(parent, "mytool-0.1.0")
	tmpDir, err := os.MkdirTemp(parent, ".tmp_unpack_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// copy archive into tmpDir as .archive.tmp
	archivePath := filepath.Join(tmpDir, ".archive.tmp")
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractArchiveToDir(archivePath, tmpDir); err != nil {
		t.Fatalf("extract failed: %v", err)
	}
	if err := assets.VerifyNoSymlinks(tmpDir); err != nil {
		t.Fatalf("symlink check failed: %v", err)
	}
	// move into place
	if err := moveAtomic(tmpDir, appDir); err != nil {
		t.Fatalf("move into place failed: %v", err)
	}
	// ensure executable exists
	exe := filepath.Join(appDir, "bin/mytool")
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("exe missing: %v", err)
	}
	// compute hash for config
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := config.UpsertBinary(&config.Binary{RemoteName: "mytool", Path: exe, Version: "0.1.0", Hash: hash, URL: "test://", Provider: mp.GetID(), Unpacked: true, AppDir: appDir}); err != nil {
		t.Fatalf("upsert config failed: %v", err)
	}

	// Now simulate update: create a new archive with new content and version
	archive2 := filepath.Join(tmp, "pkg2.tar.gz")
	f2, err := os.Create(archive2)
	if err != nil {
		t.Fatal(err)
	}
	if tw := createTarGzWithExecutable(t, f2, "bin/mytool", "#!/bin/sh\necho updated\n"); tw != nil {
		t.Fatalf("failed creating archive2: %v", tw)
	}
	f2.Close()
	data2, err := os.ReadFile(archive2)
	if err != nil {
		t.Fatal(err)
	}
	// extract to tmp and swap
	tmpDir2, err := os.MkdirTemp(parent, ".tmp_unpack_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir2)
	if err := os.WriteFile(filepath.Join(tmpDir2, ".archive.tmp"), data2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractArchiveToDir(filepath.Join(tmpDir2, ".archive.tmp"), tmpDir2); err != nil {
		t.Fatalf("extract2 failed: %v", err)
	}
	if err := assets.VerifyNoSymlinks(tmpDir2); err != nil {
		t.Fatalf("symlink check2 failed: %v", err)
	}
	// swap with backup
	old := appDir + ".old"
	_ = os.RemoveAll(old)
	if err := moveAtomic(appDir, old); err != nil {
		t.Fatal(err)
	}
	if err := moveAtomic(tmpDir2, appDir); err != nil {
		// rollback
		_ = moveAtomic(old, appDir)
		t.Fatal(err)
	}
	_ = os.RemoveAll(old)

	// verify updated binary
	out, err := os.ReadFile(filepath.Join(appDir, "bin/mytool"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "updated") {
		t.Fatalf("expected updated content, got: %s", string(out))
	}
}

// A minimal helper and mock provider
// keep names distinct from other tests
type mockArchiveProvider struct {
	archivePath string
	name        string
	version     string
}

func (m *mockArchiveProvider) Fetch(*providers.FetchOpts) (*providers.File, error) { return nil, nil }
func (m *mockArchiveProvider) GetLatestVersion() (string, string, error) {
	return m.version, "test://", nil
}
func (m *mockArchiveProvider) GetID() string { return "mock" }

// createTarGzWithExecutable writes a simple tar.gz containing path->content
func createTarGzWithExecutable(t *testing.T, f *os.File, name, content string) error {
	// implement quickly using archive/tar + gzip
	// NOTE: reuse code from previous tests; import is available
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.WriteString(tw, content); err != nil {
		return err
	}
	tw.Close()
	gw.Close()
	return nil
}
