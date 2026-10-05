package cmd

import (
	assetsPkg "github.com/marcosnils/bin2/pkg/assets"
	"os"
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
