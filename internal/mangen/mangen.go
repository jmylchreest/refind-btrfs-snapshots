//go:build gendocs

// Package mangen walks a cobra command tree and emits one combined man page
// per binary via go-md2man, instead of cobra/doc's default one-file-per-command.
// Gated behind the `gendocs` build tag so md2man stays out of release binaries.
package mangen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cpuguy83/go-md2man/v2/md2man"
	"github.com/spf13/cobra"
)

// Write renders root and every visible subcommand into <dir>/<root-name>.1
// as a single combined man page.
func Write(root *cobra.Command, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	md := buildMarkdown(root)
	roff := md2man.Render([]byte(md))
	out := filepath.Join(dir, root.Name()+".1")
	if err := os.WriteFile(out, roff, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}

func buildMarkdown(root *cobra.Command) string {
	var b strings.Builder

	// md2man infers .TH from "# <name> 1" + the NAME block; no pandoc header needed.
	fmt.Fprintf(&b, "# %s 1\n\n", root.Name())
	fmt.Fprintf(&b, "## NAME\n\n%s - %s\n\n", root.Name(), strings.TrimSpace(root.Short))

	fmt.Fprintf(&b, "## SYNOPSIS\n\n**%s** \\[*global options*\\] *COMMAND* \\[*args*\\]\n\n", root.Name())

	if long := strings.TrimSpace(root.Long); long != "" {
		fmt.Fprintf(&b, "## DESCRIPTION\n\n%s\n\n", long)
	}

	if flags := flagBlock(root.PersistentFlags().FlagUsages()); flags != "" {
		fmt.Fprintf(&b, "## GLOBAL OPTIONS\n\n```\n%s```\n\n", flags)
	}

	subs := visibleSubcommands(root)
	if len(subs) > 0 {
		b.WriteString("## COMMANDS\n\n")
		for _, c := range subs {
			writeCommand(&b, c, 3)
		}
	}

	if see := seeAlsoLine(root); see != "" {
		fmt.Fprintf(&b, "## SEE ALSO\n\n%s\n\n", see)
	}

	return b.String()
}

// writeCommand emits a single command's section at the given header depth
// (2 for top-level, 3 for nested under a parent). Recurses for sub-subcommands.
func writeCommand(b *strings.Builder, c *cobra.Command, depth int) {
	hdr := strings.Repeat("#", depth)
	fmt.Fprintf(b, "%s %s\n\n", hdr, c.CommandPath())

	if short := strings.TrimSpace(c.Short); short != "" {
		fmt.Fprintf(b, "%s\n\n", short)
	}
	if long := strings.TrimSpace(c.Long); long != "" && long != strings.TrimSpace(c.Short) {
		fmt.Fprintf(b, "%s\n\n", long)
	}

	usage := strings.TrimSpace(c.UseLine())
	if usage != "" {
		fmt.Fprintf(b, "**Usage:** `%s`\n\n", usage)
	}

	// Local flags only — global ones already rendered up top.
	if flags := flagBlock(c.LocalFlags().FlagUsages()); flags != "" {
		fmt.Fprintf(b, "**Options:**\n\n```\n%s```\n\n", flags)
	}

	for _, sub := range visibleSubcommands(c) {
		writeCommand(b, sub, depth+1)
	}
}

func visibleSubcommands(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// flagBlock trims trailing whitespace but preserves cobra's flag-table
// formatting (column alignment, type hints, defaults).
func flagBlock(s string) string {
	s = strings.TrimRight(s, " \n\t")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// seeAlsoLine cross-references the other binaries shipped from this repo so
// each man page points at its siblings.
func seeAlsoLine(root *cobra.Command) string {
	siblings := map[string][]string{
		"refind-btrfs-snapshots": {"bls-btrfs-snapshots(1)", "kernel-spy(1)"},
		"bls-btrfs-snapshots":    {"refind-btrfs-snapshots(1)", "kernel-spy(1)"},
	}
	if list, ok := siblings[root.Name()]; ok {
		return strings.Join(list, ", ")
	}
	return ""
}
