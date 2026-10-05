package cmd

import (
	"errors"
	"fmt"
	assetsPkg "github.com/marcosnils/bin2/pkg/assets"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// findExecutablesInDir scans a directory for plausible executables. It returns
// paths relative to dir.
func findExecutablesInDir(dir string) ([]string, error) {
	var out []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		// On Windows, look for .exe
		if runtime.GOOS == "windows" {
			if strings.HasSuffix(strings.ToLower(info.Name()), ".exe") {
				out = append(out, rel)
			}
			return nil
		}
		// On Unix, check executable bit
		if info.Mode()&0o111 != 0 {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// extractArchiveToDir wraps the assets extraction helper.
func extractArchiveToDir(archivePath, destDir string) error {
	// delegate to assets package implementation
	return assetsPkg.ExtractArchiveToDir(archivePath, destDir)
}

// moveAtomic attempts to atomically move src to dst. If os.Rename fails
// (e.g., EXDEV across filesystems), it falls back to a copy-and-remove
// strategy. Works for files and directories.
// moveAtomicImpl implements the atomic move with cross-filesystem fallback.
func moveAtomicImpl(src, dst string) error {
    if err := renameFunc(src, dst); err == nil {
        return nil
    }
    // Fallback: create a temporary directory on the destination filesystem
    // and copy into it, fsync contents, then rename into place.
    if _, statErr := os.Stat(dst); statErr == nil {
        return fmt.Errorf("destination already exists: %s", dst)
    }

    si, err := os.Stat(src)
    if err != nil {
        return err
    }

    dstParent := filepath.Dir(dst)
    tmpDest, err := os.MkdirTemp(dstParent, ".tmp_move_dest_*")
    if err != nil {
        return fmt.Errorf("could not create temp dir on destination: %w", err)
    }
    // clean up temp dir on errors
    cleanup := func(err error) error {
        _ = os.RemoveAll(tmpDest)
        return err
    }

    if si.IsDir() {
        // Copy into a directory inside tmpDest with the final basename
        finalName := filepath.Base(dst)
        tmpEntry := filepath.Join(tmpDest, finalName)
        if err := copyDir(src, tmpEntry); err != nil {
            return cleanup(fmt.Errorf("copy fallback failed: %w", err))
        }
        if err := fsyncTree(tmpEntry); err != nil {
            return cleanup(fmt.Errorf("fsync failed: %w", err))
        }
        if err := renameFunc(tmpEntry, dst); err != nil {
            return cleanup(fmt.Errorf("rename of temp dest failed: %w", err))
        }
        _ = os.RemoveAll(tmpDest)
        return os.RemoveAll(src)
    }

    // src is a file. Copy to tmpDest/<basename>
    base := filepath.Base(dst)
    tmpFile := filepath.Join(tmpDest, base)
    if err := copyFile(src, tmpFile); err != nil {
        return cleanup(fmt.Errorf("copy fallback failed: %w", err))
    }
    if err := fsyncFile(tmpFile); err != nil {
        return cleanup(fmt.Errorf("fsync failed: %w", err))
    }
    if err := renameFunc(tmpFile, dst); err != nil {
        return cleanup(fmt.Errorf("rename of temp dest failed: %w", err))
    }
    _ = os.RemoveAll(tmpDest)
    return os.Remove(src)
}

// moveAtomic is a variable so tests can override behavior to simulate failures.
var moveAtomic = moveAtomicImpl

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing to copy symlink")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

// ensureNoSymlinkInPath checks that no component between base (inclusive)
// and target (inclusive) is a symlink. Returns error if a symlink is found.
func ensureNoSymlinkInPathImpl(base, target string) error {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return err
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("target is outside base")
	}
	cur := base
	if rel == "." {
		st, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in path: %s", cur)
		}
		return nil
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	for _, p := range parts {
		cur = path.Join(cur, p)
		st, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in path: %s", cur)
		}
	}
	return nil
}

// ensureNoSymlinkInPath is a variable wrapper so tests can override it if needed.
var ensureNoSymlinkInPath = ensureNoSymlinkInPathImpl
