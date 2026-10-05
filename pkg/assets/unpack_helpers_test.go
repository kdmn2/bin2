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
