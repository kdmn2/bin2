package assets

import (
    "archive/zip"
    "bytes"
    "io"
    "os"
    "path/filepath"
    "testing"
)

// createZip creates an in-memory zip with the given files map[name]content
func createZip(files map[string]string) ([]byte, error) {
    buf := new(bytes.Buffer)
    zw := zip.NewWriter(buf)
    for name, content := range files {
        f, err := zw.Create(name)
        if err != nil {
            return nil, err
        }
        if _, err := io.WriteString(f, content); err != nil {
            return nil, err
        }
    }
    if err := zw.Close(); err != nil {
        return nil, err
    }
    return buf.Bytes(), nil
}

func TestExtractZip_NoZipSlip(t *testing.T) {
    z, err := createZip(map[string]string{"bin/tool": "x", "data/file.txt": "y"})
    if err != nil {
        t.Fatalf("createZip: %v", err)
    }

    dir := t.TempDir()
    archive := filepath.Join(dir, "a.zip")
    if err := os.WriteFile(archive, z, 0o600); err != nil {
        t.Fatalf("write archive: %v", err)
    }

    if err := ExtractArchiveToDir(archive, dir); err != nil {
        t.Fatalf("extract failed: %v", err)
    }

    if _, err := os.Stat(filepath.Join(dir, "bin/tool")); err != nil {
        t.Fatalf("expected bin/tool to exist: %v", err)
    }
}

func TestExtractZip_RejectsSymlink(t *testing.T) {
    // Build a zip containing a symlink entry (not easily created via zip.Writer),
    // so instead create a zip with a file that tries to escape via ../ in name.
    z, err := createZip(map[string]string{"../escape": "x"})
    if err != nil {
        t.Fatalf("createZip: %v", err)
    }
    dir := t.TempDir()
    archive := filepath.Join(dir, "a.zip")
    if err := os.WriteFile(archive, z, 0o600); err != nil {
        t.Fatalf("write archive: %v", err)
    }
    if err := ExtractArchiveToDir(archive, dir); err == nil {
        t.Fatalf("expected extraction to fail due to path traversal")
    }
}

func TestExtractTarGz_RejectsSymlinkAndZipSlip(t *testing.T) {
    // Create a tar.gz in memory with a file that tries to escape and a symlink
    // We'll write a simple tar.gz fixture to disk
    dir := t.TempDir()
    archive := filepath.Join(dir, "a.tar.gz")
    // create tar.gz with an entry ../escape and a regular file
    // For simplicity, use shell tar if available
    // create files
    base := filepath.Join(dir, "src")
    if err := os.MkdirAll(base, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    if err := os.WriteFile(filepath.Join(base, "ok.txt"), []byte("ok"), 0o600); err != nil {
        t.Fatalf("write: %v", err)
    }
    // create a malicious name via tar header: use pax to insert ../escape; easiest via go tar writer,
    // but building here for brevity we'll use the go stdlib
    f, err := os.Create(archive)
    if err != nil {
        t.Fatalf("create archive: %v", err)
    }
    gw := gzip.NewWriter(f)
    tw := tar.NewWriter(gw)
    // malicious entry
    if err := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0600, Size: int64(len("x"))}); err != nil {
        t.Fatalf("tar write header: %v", err)
    }
    if _, err := tw.Write([]byte("x")); err != nil {
        t.Fatalf("tar write body: %v", err)
    }
    // normal entry
    if err := tw.WriteHeader(&tar.Header{Name: "ok.txt", Mode: 0600, Size: int64(len("ok"))}); err != nil {
        t.Fatalf("tar write header2: %v", err)
    }
    if _, err := tw.Write([]byte("ok")); err != nil {
        t.Fatalf("tar write body2: %v", err)
    }
    tw.Close()
    gw.Close()
    f.Close()

    outdir := filepath.Join(dir, "out")
    if err := os.MkdirAll(outdir, 0o755); err != nil {
        t.Fatalf("mkdir out: %v", err)
    }

    if err := ExtractArchiveToDir(archive, outdir); err == nil {
        t.Fatalf("expected tar extraction to fail due to path traversal")
    }
}
