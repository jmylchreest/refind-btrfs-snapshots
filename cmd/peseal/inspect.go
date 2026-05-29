package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/authenticode"
	"github.com/jmylchreest/refind-btrfs-snapshots/pkg/uki"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect [flags] FILE...",
	Short: "Print signature and PE/UKI metadata for files",
	Long: `Report Authenticode signature status, PE32+ header layout,
section table, and (for UKIs) decoded .cmdline / .uname / .osrel
content per file. Use --json for a structured form suitable for
scripting.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runInspect,
}

func init() {
	rootCmd.AddCommand(inspectCmd)
	inspectCmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of text")
}

type inspectOptions struct {
	JSON bool
	Out  io.Writer
}

type inspectResult struct {
	Inspected int
	Failed    int
}

// inspectReport is the typed shape emitted under --json. The text formatter
// also walks this struct so the two outputs stay in lock-step on content.
type inspectReport struct {
	File      string                   `json:"file"`
	Size      int64                    `json:"size"`
	PE        peReport                 `json:"pe"`
	Signature signatureReport          `json:"signature"`
	Sections  []sectionReport          `json:"sections"`
	UKI       ukiReport                `json:"uki"`
}

type peReport struct {
	Type     string `json:"type"`
	Sections int    `json:"sections"`
}

type signatureReport struct {
	Signed       bool   `json:"signed"`
	SignerCN     string `json:"signer_cn,omitempty"`
	SignatureAlg string `json:"signature_alg,omitempty"`
	DigestAlg    string `json:"digest_alg"`
	DigestHex    string `json:"digest_hex"`
}

type sectionReport struct {
	Name            string `json:"name"`
	VirtualAddress  uint32 `json:"virtual_address"`
	VirtualSize     uint32 `json:"virtual_size"`
	Characteristics uint32 `json:"characteristics"`
	Size            int    `json:"size"`
}

type ukiReport struct {
	IsUKI     bool              `json:"is_uki"`
	Cmdline   string            `json:"cmdline,omitempty"`
	Uname     string            `json:"uname,omitempty"`
	OSRelease map[string]string `json:"os_release,omitempty"`
	Profiles  []uki.Profile     `json:"profiles,omitempty"`
}

func runInspect(cmd *cobra.Command, args []string) error {
	opts := inspectOptions{Out: os.Stdout}
	opts.JSON, _ = cmd.Flags().GetBool("json")

	res := inspectFiles(opts, args)
	if res.Inspected == 0 && res.Failed > 0 {
		return fmt.Errorf("inspect: all %d files failed", res.Failed)
	}
	return nil
}

func inspectFiles(opts inspectOptions, paths []string) inspectResult {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	var res inspectResult
	for _, p := range paths {
		report, err := buildReport(p)
		if err != nil {
			res.Failed++
			log.Warn().Err(err).Str("path", p).Msg("inspect failed")
			continue
		}
		res.Inspected++
		if opts.JSON {
			enc := json.NewEncoder(opts.Out)
			enc.SetIndent("", "  ")
			_ = enc.Encode(report)
		} else {
			writeText(opts.Out, report)
		}
	}
	return res
}

func buildReport(path string) (*inspectReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	sig, err := authenticode.Inspect(data)
	if err != nil {
		return nil, fmt.Errorf("authenticode: %w", err)
	}

	img, err := uki.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("uki/pe: %w", err)
	}

	report := &inspectReport{
		File: path,
		Size: info.Size(),
		PE:   peReport{Type: "PE32+", Sections: len(img.Sections())},
		Signature: signatureReport{
			Signed:       sig.Signed,
			SignerCN:     sig.SignerCN,
			SignatureAlg: sig.SignatureAlg,
			DigestAlg:    sig.DigestAlg,
			DigestHex:    sig.DigestHex,
		},
	}

	hasLinux := false
	for _, s := range img.Sections() {
		if s.Name == uki.SectionLinux {
			hasLinux = true
		}
		report.Sections = append(report.Sections, sectionReport{
			Name:            s.Name,
			VirtualAddress:  s.VirtualAddress,
			VirtualSize:     uint32(len(s.Data)),
			Characteristics: s.Characteristics,
			Size:            len(s.Data),
		})
	}

	if hasLinux {
		report.UKI.IsUKI = true
		report.UKI.Cmdline = img.Cmdline()
		report.UKI.Uname = img.Uname()
		report.UKI.OSRelease = img.OSRelease()
		report.UKI.Profiles = img.Profiles()
	}

	return report, nil
}

func writeText(w io.Writer, r *inspectReport) {
	fmt.Fprintf(w, "File: %s\n", r.File)
	fmt.Fprintf(w, "Size: %d bytes\n\n", r.Size)

	fmt.Fprintln(w, "PE header")
	fmt.Fprintf(w, "  Type:     %s\n", r.PE.Type)
	fmt.Fprintf(w, "  Sections: %d\n\n", r.PE.Sections)

	fmt.Fprintln(w, "Signature")
	if r.Signature.Signed {
		fmt.Fprintf(w, "  Status:    signed\n")
		fmt.Fprintf(w, "  Signer CN: %s\n", r.Signature.SignerCN)
		fmt.Fprintf(w, "  SigAlg:    %s\n", r.Signature.SignatureAlg)
	} else {
		fmt.Fprintf(w, "  Status:    unsigned\n")
	}
	fmt.Fprintf(w, "  DigestAlg: %s\n", r.Signature.DigestAlg)
	fmt.Fprintf(w, "  Digest:    %s\n\n", r.Signature.DigestHex)

	fmt.Fprintln(w, "Sections")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  Name\tVirtSize\tVirtAddr\tFlags")
	for _, s := range r.Sections {
		fmt.Fprintf(tw, "  %s\t%d\t0x%08x\t0x%08x\n", s.Name, s.VirtualSize, s.VirtualAddress, s.Characteristics)
	}
	tw.Flush()
	fmt.Fprintln(w)

	if r.UKI.IsUKI {
		fmt.Fprintln(w, "UKI content")
		if r.UKI.Cmdline != "" {
			fmt.Fprintf(w, "  Cmdline: %s\n", r.UKI.Cmdline)
		}
		if r.UKI.Uname != "" {
			fmt.Fprintf(w, "  Uname:   %s\n", r.UKI.Uname)
		}
		if len(r.UKI.OSRelease) > 0 {
			fmt.Fprintln(w, "  OS Release:")
			for k, v := range r.UKI.OSRelease {
				fmt.Fprintf(w, "    %-20s = %s\n", k, v)
			}
		}
		if len(r.UKI.Profiles) > 0 {
			fmt.Fprintln(w, "  Profiles:")
			for _, p := range r.UKI.Profiles {
				fmt.Fprintf(w, "    [%d] ID=%s  Title=%s  Cmdline=%s\n", p.Index, p.ID, p.Title, p.Cmdline)
			}
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, "Not a UKI (no .linux section)")
		fmt.Fprintln(w)
	}
}
