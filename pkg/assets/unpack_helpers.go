package assets

import (
    "archive/tar"
    "archive/zip"
    "compress/gzip"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "os/exec"
    "syscall"
)

// ExtractArchiveToDir extracts common archive formats (zip, tar.gz, tar, gz)
// into destDir. It protects against zip-slip by ensuring no file escapes destDir.
func ExtractArchiveToDir(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

    // Try zip first
    if strings.HasSuffix(strings.ToLower(archivePath), ".zip") {
        if err := extractZip(f, destDir); err != nil {
            return err
        }
        return verifyNoSymlinks(destDir)
    }
	// Try gzip + tar
	if strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz") || strings.HasSuffix(strings.ToLower(archivePath), ".tgz") {
		if err := extractTarGz(f, destDir); err != nil {
			return err
		}
		return nil
	}
	// Fallback: if looks like gz only
    if strings.HasSuffix(strings.ToLower(archivePath), ".gz") {
		// attempt to ungzip to a single file
		gr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gr.Close()
		out := filepath.Join(destDir, filepath.Base(strings.TrimSuffix(archivePath, ".gz")))
		of, err := os.Create(out)
		if err != nil {
			return err
		}
		defer of.Close()
		if _, err := io.Copy(of, gr); err != nil {
			return err
		}
        return nil
    }

    // Try 7z
    if strings.HasSuffix(strings.ToLower(archivePath), ".7z") {
        if err := extract7z(archivePath, destDir); err != nil {
            return err
        }
        return verifyNoSymlinks(destDir)
    }

    // As a last resort, try unzip (some archives might have no extension)
    if err := extractZip(f, destDir); err == nil {
        if err := verifyNoSymlinks(destDir); err != nil {
            return err
        }
        return nil
    }
    // unsupported
    return fmt.Errorf("unsupported archive format")
}

func extractZip(r io.ReaderAt, destDir string) error {
	rf, ok := r.(*os.File)
	if !ok {
		return fmt.Errorf("zip extraction requires file handle")
	}
	fi, err := rf.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(rf, fi.Size())
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		// Reject symlinks inside archives for safety
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains symlink %s, refusing to extract", f.Name)
		}
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in archive: %s", f.Name)
		}
        if f.FileInfo().IsDir() {
            if err := os.MkdirAll(target, f.Mode()); err != nil {
                return err
            }
            continue
        }
        if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
            return err
        }
        rc, err := f.Open()
        if err != nil {
            return err
        }
        // Use O_EXCL to avoid following existing symlinks and to fail if file exists.
        of, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.Mode())
        if err != nil {
            rc.Close()
            return err
        }
        _, err = io.Copy(of, rc)
        of.Close()
        rc.Close()
        if err != nil {
            return err
        }
        // Sanity check: ensure file is not a symlink
        st, lerr := os.Lstat(target)
        if lerr != nil {
            return lerr
        }
        if st.Mode()&os.ModeSymlink != 0 {
            return fmt.Errorf("extracted file is a symlink: %s", target)
        }
    }
    return nil
}

func extractTarGz(f *os.File, destDir string) error {
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in archive: %s", hdr.Name)
		}
		// Reject symlinks and hard links for safety
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			return fmt.Errorf("archive contains link %s, refusing to extract", hdr.Name)
		}
        if hdr.FileInfo().IsDir() {
            if err := os.MkdirAll(target, hdr.FileInfo().Mode()); err != nil {
                return err
            }
            continue
        }
        if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
            return err
        }
        // Use O_EXCL to avoid following symlinks and to fail if file exists
        of, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, hdr.FileInfo().Mode())
        if err != nil {
            return err
        }
        if _, err := io.Copy(of, tr); err != nil {
            of.Close()
            return err
        }
        of.Close()
        // Sanity check
        st, lerr := os.Lstat(target)
        if lerr != nil {
            return lerr
        }
        if st.Mode()&os.ModeSymlink != 0 {
            return fmt.Errorf("extracted file is a symlink: %s", target)
        }
    }
    return nil
}

// extract7z uses an external 7z/7za binary to extract archives.
// It tries several common executables until one succeeds.
func extract7z(archivePath, destDir string) error {
    try := []string{"7z", "7za", "7zr"}
    for _, cmd := range try {
        // 7z x -y -oDEST ARCHIVE
        c := exec.Command(cmd, "x", "-y", "-o"+destDir, archivePath)
        // ensure no extra environment is passed that could affect behavior
        c.Env = append(os.Environ(), "PATH="+os.Getenv("PATH"))
        if out, err := c.CombinedOutput(); err != nil {
            // if command not found, try next
            if ee, ok := err.(*exec.Error); ok && ee.Err == exec.ErrNotFound {
                continue
            }
            // some 7z versions return non-zero on warnings; treat as error
            return fmt.Errorf("7z extraction failed: %v: %s", err, string(out))
        }
        return nil
    }
    return fmt.Errorf("7z executable not found")
}

// verifyNoSymlinks walks destDir and returns an error if any symlink is found.
func verifyNoSymlinks(destDir string) error {
    return filepath.Walk(destDir, func(p string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        if info.Mode()&os.ModeSymlink != 0 {
            return fmt.Errorf("extracted tree contains symlink: %s", p)
        }
        return nil
    })
}

// VerifyNoSymlinks is an exported wrapper so other packages can assert
// the extracted tree contains no symlinks.
func VerifyNoSymlinks(destDir string) error {
    return verifyNoSymlinks(destDir)
}
