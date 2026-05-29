package main

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/authenticode"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var signCmd = &cobra.Command{
	Use:   "sign [flags] [FILE...]",
	Short: "Sign PE32+ binaries with the configured key and certificate",
	Long: `Sign each named FILE (or every file matching the configured paths
when no FILE is given) with the configured key + certificate. Idempotent
by default: files already signed by the configured cert are skipped.`,
	RunE: runSign,
}

func init() {
	rootCmd.AddCommand(signCmd)
	signCmd.Flags().StringP("key", "k", "", "Path to PEM-encoded RSA private key (overrides config)")
	signCmd.Flags().String("cert", "", "Path to PEM-encoded X.509 certificate (overrides config)")
	signCmd.Flags().Bool("dry-run", false, "Show what would be signed without writing")
	signCmd.Flags().BoolP("yes", "y", false, "Auto-approve (no-op today; reserved for future confirm UX)")
	signCmd.Flags().Bool("no-skip-signed", false, "Always re-sign, even if the file already carries a valid signature for the configured cert")
}

// signOptions decouples runSign (CLI plumbing) from signFiles (pure logic)
// so the latter is reachable from tests without spinning up cobra.
type signOptions struct {
	KeyPath           string
	CertPath          string
	DryRun            bool
	SkipAlreadySigned bool
}

// signResult counts outcomes across a batch. Signed + Skipped + Failed
// always equals the number of input files; Planned is set only in
// DryRun and tracks how many would have been signed.
type signResult struct {
	Signed  int
	Skipped int
	Failed  int
	Planned int
}

func runSign(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	opts := signOptions{
		KeyPath:           cfg.KeyPath,
		CertPath:          cfg.CertPath,
		SkipAlreadySigned: cfg.SkipAlreadySigned.IsTrue(),
	}
	if k, _ := cmd.Flags().GetString("key"); k != "" {
		opts.KeyPath = k
	}
	if c, _ := cmd.Flags().GetString("cert"); c != "" {
		opts.CertPath = c
	}
	if d, _ := cmd.Flags().GetBool("dry-run"); d {
		opts.DryRun = true
	}
	if force, _ := cmd.Flags().GetBool("no-skip-signed"); force {
		opts.SkipAlreadySigned = false
	}

	paths := args
	if len(paths) == 0 {
		paths = expandPaths(cfg.Paths)
	} else {
		paths = expandPaths(paths)
	}
	if len(paths) == 0 {
		log.Warn().Msg("No files to sign — provide arguments or set 'paths' in config")
		return nil
	}

	res, err := signFiles(opts, paths)
	logSignSummary(opts, res)
	return err
}

// signFiles is the testable core: given options + an explicit file list,
// sign or skip each in turn. Returns the aggregate counts. The returned
// error is non-nil iff any file failed; individual failures are logged
// inline so a partial-success batch still shows the working files.
func signFiles(opts signOptions, paths []string) (signResult, error) {
	var res signResult

	var signer *authenticode.Signer
	if !opts.DryRun {
		s, err := authenticode.NewSignerFromFiles(opts.KeyPath, opts.CertPath)
		if err != nil {
			return res, err
		}
		signer = s
	}

	var trustRoots = loadVerifyRoots(opts.CertPath)
	var errs []error

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			res.Failed++
			errs = append(errs, fmt.Errorf("read %s: %w", p, err))
			log.Warn().Err(err).Str("path", p).Msg("Could not read file")
			continue
		}

		if opts.SkipAlreadySigned && len(trustRoots) > 0 {
			if authenticode.Verify(data, trustRoots) == nil {
				res.Skipped++
				log.Info().Str("path", p).Msg("Already signed by configured cert, skipping")
				continue
			}
		}

		if opts.DryRun {
			res.Planned++
			log.Info().Str("path", p).Msg("[DRY RUN] Would sign")
			continue
		}

		signed, err := signer.Sign(data)
		if err != nil {
			res.Failed++
			errs = append(errs, fmt.Errorf("sign %s: %w", p, err))
			log.Warn().Err(err).Str("path", p).Msg("Sign failed")
			continue
		}
		if err := os.WriteFile(p, signed, 0o644); err != nil {
			res.Failed++
			errs = append(errs, fmt.Errorf("write %s: %w", p, err))
			log.Warn().Err(err).Str("path", p).Msg("Write failed")
			continue
		}
		res.Signed++
		log.Info().Str("path", p).Msg("Signed")
	}

	if len(errs) > 0 {
		return res, fmt.Errorf("peseal sign: %d of %d files failed: %w", len(errs), len(paths), errors.Join(errs...))
	}
	return res, nil
}

func logSignSummary(opts signOptions, res signResult) {
	if opts.DryRun {
		log.Info().Int("planned", res.Planned).Int("failed", res.Failed).Msg("[DRY RUN] sign summary")
		return
	}
	log.Info().Int("signed", res.Signed).Int("skipped", res.Skipped).Int("failed", res.Failed).Msg("sign summary")
}

// expandPaths runs filepath.Glob on each input, drops entries that don't
// resolve to a regular file, and deduplicates. Paths that resolve to
// directories are silently dropped — sign operates on files.
func expandPaths(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, pat := range in {
		matches, err := filepath.Glob(pat)
		if err != nil || len(matches) == 0 {
			continue
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || info.IsDir() {
				continue
			}
			if seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// loadVerifyRoots reads certPath and returns it as a trust root for
// idempotency checks. Returns nil if certPath is empty or unreadable —
// callers then skip the already-signed shortcut and sign unconditionally.
func loadVerifyRoots(certPath string) []*x509.Certificate {
	if certPath == "" {
		return nil
	}
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		// Try raw DER as a last resort.
		if c, err := x509.ParseCertificate(raw); err == nil {
			return []*x509.Certificate{c}
		}
		return nil
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return []*x509.Certificate{c}
}
