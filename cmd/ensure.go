package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/caarlos0/log"
	"github.com/fatih/color"
	"github.com/marcosnils/bin2/pkg/config"
    "github.com/marcosnils/bin2/pkg/options"
    "github.com/marcosnils/bin2/pkg/assets"
	"github.com/marcosnils/bin2/pkg/providers"
	"github.com/spf13/cobra"
)

type ensureCmd struct {
	cmd *cobra.Command
}

func newEnsureCmd() *ensureCmd {
	root := &ensureCmd{}
	// nolint: dupl
	cmd := &cobra.Command{
		Use:           "ensure [binary_path]...",
		Aliases:       []string{"e"},
		Short:         "Ensures that all binaries listed in the configuration are present",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Get()
			binsToProcess := map[string]*config.Binary{}

			// Update specific binaries
			if len(args) > 0 {
				for _, a := range args {
					bin, err := getBinPath(a)
					if err != nil {
						return err
					}
					binsToProcess[bin] = cfg.Bins[bin]
				}
			} else {
				binsToProcess = cfg.Bins
			}

			// TODO: code smell here, this pretty much does
			// the same thing as install logic. Refactor to
			// use the same code in both places
			for _, binCfg := range binsToProcess {
				ep := os.ExpandEnv(binCfg.Path)
				_, err := os.Stat(ep)

				if err == nil {
					f, err := os.Open(ep)
					if err != nil {
						return err
					}

					h := sha256.New()
					if _, err := io.Copy(h, f); err != nil {
						return err
					}

					if fmt.Sprintf("%x", h.Sum(nil)) == binCfg.Hash {
						continue
					}

					log.Infof("%s hash does not match with config's, re-installing", ep)

				} else if !os.IsNotExist(err) {
					continue
				}

				p, err := providers.New(binCfg.URL, binCfg.Provider)
				if err != nil {
					return err
				}
				log.Debugf("Using provider '%s' for '%s'", p.GetID(), binCfg.URL)

				// If unpacked, perform an unpack-aware ensure: fetch archive and
				// extract to temp dir then swap into place.
				if binCfg.Unpacked {
					appDir := binCfg.AppDir
					if appDir == "" {
						appDir = filepath.Dir(binCfg.Path)
					}

					// If appDir and chosen executable exist, skip
					chosenExec := filepath.Base(binCfg.Path)
					if _, err := os.Stat(filepath.Join(appDir, chosenExec)); err == nil {
						// exists, nothing to do
						continue
					}

					pResult, err := p.Fetch(&providers.FetchOpts{
						Version:            binCfg.Version,
						PackageName:        binCfg.RemoteName,
						PackagePath:        binCfg.PackagePath,
						PreviousAsset:      binCfg.SelectedAsset,
						PreviousVersion:    binCfg.Version,
						AutoSelectPrevious: true,
						Unpack:             true,
					})
					if err != nil {
						return err
					}

					parent := filepath.Dir(appDir)
					tmpDir, err := os.MkdirTemp(parent, ".tmp_unpack_*")
					if err != nil {
						return fmt.Errorf("error creating temp dir for extraction: %w", err)
					}
					defer func() { _ = os.RemoveAll(tmpDir) }()

					archivePath := filepath.Join(tmpDir, ".archive.tmp")
					af, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
					if err != nil {
						return fmt.Errorf("error creating temp archive file: %w", err)
					}
					if _, err := io.Copy(af, pResult.Data); err != nil {
						af.Close()
						return fmt.Errorf("error writing archive to disk: %w", err)
					}
					af.Close()

                    if err := extractArchiveToDir(archivePath, tmpDir); err != nil {
                        return fmt.Errorf("error extracting archive: %w", err)
                    }
                    if err := assets.VerifyNoSymlinks(tmpDir); err != nil {
                        return fmt.Errorf("extracted archive failed safety checks: %w", err)
                    }

					execs, err := findExecutablesInDir(tmpDir)
					if err != nil {
						return fmt.Errorf("error scanning for executables: %w", err)
					}
					if len(execs) == 0 {
						return fmt.Errorf("no executable found inside the archive")
					}
					// If multiple, try to pick the same executable basename as before
					var chosen string
					if len(execs) == 1 {
						chosen = execs[0]
					} else {
						// prefer the one matching previous package path if available
						for _, e := range execs {
							if filepath.Base(e) == chosenExec {
								chosen = e
								break
							}
						}
						if chosen == "" {
							opts := make([]fmt.Stringer, len(execs))
							for i, e := range execs {
								opts[i] = options.LiteralStringer(e)
							}
							choice, err := options.SelectWithDefault("Multiple executables found, select the entrypoint:", opts, -1)
							if err != nil {
								return err
							}
							chosen = choice.(fmt.Stringer).String()
						}
					}

					// swap
					var oldApp string
					if _, err := os.Stat(appDir); err == nil {
						oldApp = appDir + ".old"
						_ = os.RemoveAll(oldApp)
						if err := os.Rename(appDir, oldApp); err != nil {
							return fmt.Errorf("error moving existing app dir aside: %w", err)
						}
					}
					if err := os.Rename(tmpDir, appDir); err != nil {
						if oldApp != "" {
							_ = os.Rename(oldApp, appDir)
						}
						return fmt.Errorf("error moving extracted app into place: %w", err)
					}
					if oldApp != "" {
						_ = os.RemoveAll(oldApp)
					}

					absPath := filepath.Join(appDir, chosen)
					absPath, err = filepath.Abs(absPath)
					if err != nil {
						return fmt.Errorf("error converting to absolute path: %w", err)
					}

					// compute archive hash
					archiveBytes, _ := os.ReadFile(archivePath)
					err = config.UpsertBinary(&config.Binary{
						RemoteName:    pResult.Name,
						Path:          absPath,
						Version:       pResult.Version,
						Hash:          fmt.Sprintf("%x", sha256.Sum256(archiveBytes)),
						URL:           binCfg.URL,
						Provider:      p.GetID(),
						PackagePath:   pResult.PackagePath,
						SelectedAsset: pResult.SelectedAsset,
						Pinned:        binCfg.Pinned,
						Unpacked:      true,
						AppDir:        appDir,
					})
					if err != nil {
						return err
					}
					log.Infof("Done ensuring %s to %s", os.ExpandEnv(binCfg.Path), color.GreenString(binCfg.Version))
					continue
				}

				pResult, err := p.Fetch(&providers.FetchOpts{
					Version:            binCfg.Version,
					PackageName:        binCfg.RemoteName,
					PackagePath:        binCfg.PackagePath,
					PreviousAsset:      binCfg.SelectedAsset,
					PreviousVersion:    binCfg.Version,
					AutoSelectPrevious: true,
				})
				if err != nil {
					return err
				}

				hash, err := saveToDisk(pResult, ep, true)
				if err != nil {
					return fmt.Errorf("error installing binary: %w", err)
				}

				err = config.UpsertBinary(&config.Binary{
					RemoteName:    pResult.Name,
					Path:          binCfg.Path,
					Version:       pResult.Version,
					Hash:          fmt.Sprintf("%x", hash),
					URL:           binCfg.URL,
					Provider:      p.GetID(),
					PackagePath:   pResult.PackagePath,
					SelectedAsset: pResult.SelectedAsset,
					Pinned:        binCfg.Pinned,
				})
				if err != nil {
					return err
				}
				log.Infof("Done ensuring %s to %s", os.ExpandEnv(binCfg.Path), color.GreenString(binCfg.Version))
			}
			return nil
		},
	}

	root.cmd = cmd
	return root
}
