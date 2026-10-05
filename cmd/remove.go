package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/marcosnils/bin2/pkg/config"
	"github.com/spf13/cobra"
)

type removeCmd struct {
	cmd *cobra.Command
}

func newRemoveCmd() *removeCmd {
	root := &removeCmd{}
	// nolint: dupl
	cmd := &cobra.Command{
		Use:           "remove [<name> | <paths...>]",
		Aliases:       []string{"rm"},
		Short:         "Removes binaries managed by bin",
		SilenceUsage:  true,
		Args:          cobra.MinimumNArgs(1),
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Get()

			existingToRemove := []string{}

			bins := cfg.Bins

			for _, p := range args {
				// TODO: avoid calling getBinPath each time and make it
				// once at the beginning for each arg
				bp, err := getBinPath(p)

				if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(os.Stderr, "binary %s not found in PATH, skipping\n", p)
				} else if err != nil {
					return err
				}
				ebp := os.ExpandEnv(bp)
				if binCfg, ok := bins[ebp]; ok {
					existingToRemove = append(existingToRemove, ebp)

					// If this binary was an unpacked install, remove the whole AppDir
                    if binCfg.Unpacked && binCfg.AppDir != "" {
                        // Basic sanity: do not remove root or home
                        if binCfg.AppDir == "/" || binCfg.AppDir == "" {
                            return fmt.Errorf("refusing to remove unsafe AppDir: %s", binCfg.AppDir)
                        }
                        // Ensure no symlink in the path we're about to remove
                        if err := ensureNoSymlinkInPath(filepath.Dir(binCfg.AppDir), binCfg.AppDir); err != nil {
                            return fmt.Errorf("refusing to remove unsafe AppDir (symlink detected): %w", err)
                        }
                        if err := os.RemoveAll(binCfg.AppDir); err != nil && !os.IsNotExist(err) {
                            return fmt.Errorf("error removing app directory %s: %v", binCfg.AppDir, err)
                        }
					} else {
						// Normal single-file installation: remove the file
						if err := os.Remove(os.ExpandEnv(bp)); err != nil && !os.IsNotExist(err) {
							return fmt.Errorf("error removing path %s: %v", os.ExpandEnv(bp), err)
						}
					}
					continue
				}
			}
			err := config.RemoveBinaries(existingToRemove)
			return err
		},
	}

	root.cmd = cmd
	return root
}
