package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/authenticode"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify [flags] FILE...",
	Short: "Verify Authenticode signatures against a trust certificate",
	Long: `Verify each FILE's Authenticode signature against the supplied
certificate (or the configured cert_path). Exit code is non-zero if any
file fails to verify.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runVerify,
}

func init() {
	rootCmd.AddCommand(verifyCmd)
	verifyCmd.Flags().String("cert", "", "Path to trust certificate (overrides config cert_path)")
}

type verifyOptions struct {
	CertPath string
}

type verifyResult struct {
	Verified int
	Failed   int
}

func runVerify(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	opts := verifyOptions{CertPath: cfg.CertPath}
	if c, _ := cmd.Flags().GetString("cert"); c != "" {
		opts.CertPath = c
	}
	if opts.CertPath == "" {
		return errors.New("no trust certificate provided (set --cert or cert_path in config)")
	}

	res := verifyFiles(opts, args)
	log.Info().Int("verified", res.Verified).Int("failed", res.Failed).Msg("verify summary")
	if res.Failed > 0 {
		return fmt.Errorf("verify: %d of %d files failed", res.Failed, len(args))
	}
	return nil
}

// verifyFiles is the testable core for verify. Returns counts; doesn't
// propagate errors — callers introspect res.Failed for exit semantics.
func verifyFiles(opts verifyOptions, paths []string) verifyResult {
	var res verifyResult
	roots := loadVerifyRoots(opts.CertPath)
	if len(roots) == 0 {
		log.Warn().Str("cert_path", opts.CertPath).Msg("Could not load trust cert")
		res.Failed = len(paths)
		return res
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			res.Failed++
			log.Warn().Err(err).Str("path", p).Msg("Could not read")
			continue
		}
		if err := authenticode.Verify(data, roots); err != nil {
			res.Failed++
			log.Warn().Err(err).Str("path", p).Msg("Verify failed")
			continue
		}
		res.Verified++
		log.Info().Str("path", p).Msg("OK")
	}
	return res
}

