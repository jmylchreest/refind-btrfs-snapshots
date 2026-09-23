package refind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmylchreest/refind-btrfs-snapshots/internal/btrfs"
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
	"github.com/stretchr/testify/require"
)

// Expected literals follow rEFInd's documented doubled-quote convention, not
// Go/shell backslash escaping. Keep these independent of the production encoder.
func TestRefindQuotedFields(t *testing.T) {
	p := NewParser("/boot")
	for _, tc := range []struct {
		line string
		want []string
	}{
		{`"Linux" "quiet root=UUID=abc rootflags=subvol=/@,compress=zstd"`, []string{"Linux", "quiet root=UUID=abc rootflags=subvol=/@,compress=zstd"}},
		{`"Linux ""rescue""" "my_opt=""with  quotes"" initrd=\EFI\linux.img #literal" # comment`, []string{`Linux "rescue"`, `my_opt="with  quotes" initrd=\EFI\linux.img #literal`}},
		{`"" ""`, []string{"", ""}},
		{"\t\"Linux\"\t\"quiet\" # comment", []string{"Linux", "quiet"}},
	} {
		require.Equal(t, tc.want, p.parseQuotedLine(tc.line), tc.line)
	}
}

func TestRefindSerialisationRegeneration(t *testing.T) {
	g := NewGenerator("/boot", "2006-01-02", false)
	snap := &btrfs.Snapshot{Subvolume: &btrfs.Subvolume{ID: 42, Path: "/.snapshots/42/snapshot"}, SnapshotTime: time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)}
	snapshots := []*btrfs.Snapshot{snap}
	raw := `quiet root=UUID=abc rootflags=subvol=/@ my_opt="with  quotes" initrd=\EFI\linux.img`
	quoted := `"quiet root=UUID=abc rootflags=subvol=/@ my_opt=""with  quotes"" initrd=\EFI\linux.img"`
	for _, input := range []string{raw, quoted} {
		t.Run(input, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "snapshots.conf")
			content := "menuentry \"Linux \"\"rescue\"\"\" {\n\tloader \"/EFI/my kernel\"\n\toptions " + input + "\n}\n"
			require.NoError(t, os.WriteFile(path, []byte(content), 0600))
			d, err := g.GenerateManagedConfigDiff(nil, snapshots, nil, path)
			require.NoError(t, err)
			require.NotNil(t, d)
			require.Contains(t, d.Modified, `menuentry "Linux ""rescue""" {`)
			require.Contains(t, d.Modified, "    options "+quoted+"\n")
			require.Contains(t, d.Modified, `options "quiet root=UUID=abc rootflags=subvol=/@/.snapshots/42/snapshot,subvolid=42 my_opt=""with  quotes"" initrd=\EFI\linux.img"`)
			require.NoError(t, os.WriteFile(path, []byte(d.Modified), 0600))
			next, err := g.GenerateManagedConfigDiff(nil, snapshots, nil, path)
			require.NoError(t, err)
			require.Nil(t, next, "regeneration must be stable")
			entries := g.parseExistingManagedConfig(d.Modified)
			require.Equal(t, raw, entries[`Linux "rescue"`].Options)
			require.Equal(t, "/EFI/my kernel", entries[`Linux "rescue"`].Loader)
		})
	}
	// refind_linux.conf must use the same encoding for both title and command line.
	base := `"Linux ""rescue""" ` + quoted + "\n"
	result, err := g.generateRefindLinuxConfWithAllEntries(base, snapshots, []*MenuEntry{{Title: `Linux "rescue"`, Options: raw}}, nil)
	require.NoError(t, err)
	require.Contains(t, result, `"Linux ""rescue"" (2026-09-23)" "quiet root=UUID=abc rootflags=subvol=/@/.snapshots/42/snapshot,subvolid=42 my_opt=""with  quotes"" initrd=\EFI\linux.img"`)
	path := filepath.Join(t.TempDir(), "refind_linux.conf")
	require.NoError(t, os.WriteFile(path, []byte(result), 0600))
	entries, err := g.parser.parseRefindLinuxConf(path)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, raw, entries[0].Options)
	require.Equal(t, `Linux "rescue"`, entries[0].Title)
	next, err := g.UpdateRefindLinuxConfWithAllEntries(snapshots, entries[:1], nil)
	require.NoError(t, err)
	require.Nil(t, next, "refind_linux.conf regeneration must be stable")

}

func TestRefindTemplateOptionsQuoted(t *testing.T) {
	for _, detected := range []bool{false, true} {
		g := NewGenerator("/boot", "2006", false)
		if detected {
			g.bootSets = []*kernel.BootSet{{Kernel: &kernel.BootImage{Path: "/vmlinuz-linux"}}}
		}
		snapshots := []*btrfs.Snapshot{{Subvolume: &btrfs.Subvolume{ID: 42, Path: "/.snapshots/42/snapshot"}}}
		d, err := g.GenerateManagedConfigDiff(nil, snapshots, &btrfs.Filesystem{UUID: "abc"}, filepath.Join(t.TempDir(), "new.conf"))
		require.NoError(t, err)
		count := 0
		for line := range strings.SplitSeq(d.Modified, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "options ") {
				require.True(t, strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(line, "options")), `"`), line)
				require.True(t, strings.HasSuffix(line, `"`), line)
				count++
			}
		}
		require.Equal(t, 2, count)
	}
}

func TestRefindSerialiserRejectsMultilineValues(t *testing.T) {
	for _, value := range []string{"quiet\nloader evil", "quiet\r", "quiet\x00"} {
		g := NewGenerator("/boot", "2006", false)
		entry := &MenuEntry{Title: "Linux", Options: value}
		snap := &btrfs.Snapshot{Subvolume: &btrfs.Subvolume{ID: 42, Path: "/.snapshots/42/snapshot"}}
		d, err := g.GenerateManagedConfigDiff([]*MenuEntry{entry}, nil, nil, filepath.Join(t.TempDir(), "new.conf"))
		require.Error(t, err)
		require.Nil(t, d)
		output, err := g.generateRefindLinuxConfWithAllEntries("", []*btrfs.Snapshot{snap}, []*MenuEntry{entry}, nil)
		require.Error(t, err)
		require.Empty(t, output)
	}
}

func TestRefindSerialiserSpecialValues(t *testing.T) {
	g := NewGenerator("/boot", "2006", false)
	entry := &MenuEntry{Title: `Linux "rescue"`, Volume: `Disk #1, Linux`, Icon: `/icons/my "linux".png`, Loader: `/EFI/a=b/kernel`, Initrd: []string{`\EFI\my initrd.img`}, Options: `quiet #literal`}
	output, err := g.generateSingleMenuEntry(entry.Title, entry, nil, nil)
	require.NoError(t, err)
	require.Contains(t, output, `volume "Disk #1, Linux"`)
	require.Contains(t, output, `icon "/icons/my ""linux"".png"`)
	require.Contains(t, output, `loader "/EFI/a=b/kernel"`)
	require.Contains(t, output, `initrd "\EFI\my initrd.img"`)
	require.Contains(t, output, `options "quiet #literal"`)
	parsed := g.parseExistingManagedConfig(output)[entry.Title]
	require.NotNil(t, parsed)
	require.Equal(t, entry.Volume, parsed.Volume)
	require.Equal(t, entry.Icon, parsed.Icon)
	require.Equal(t, entry.Loader, parsed.Loader)
	require.Equal(t, entry.Initrd, parsed.Initrd)
	require.Equal(t, entry.Options, parsed.Options)
}
