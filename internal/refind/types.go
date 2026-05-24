// Package refind parses and generates rEFInd boot manager configuration files.
package refind

// Config represents a rEFInd configuration
type Config struct {
	Path         string       `json:"path"`
	Entries      []*MenuEntry `json:"entries"`
	IncludePaths []string     `json:"include_paths"`
	GlobalConfig []string     `json:"global_config"`
}

// MenuEntry represents a rEFInd menu entry
type MenuEntry struct {
	Title       string          `json:"title"`
	Icon        string          `json:"icon"`
	Volume      string          `json:"volume"`
	Loader      string          `json:"loader"`
	Initrd      []string        `json:"initrd"`
	Options     string          `json:"options"`
	Submenues   []*SubmenuEntry `json:"submenues,omitempty"`
	SourceFile  string          `json:"source_file"`
	LineNumber  int             `json:"line_number"`
	BootOptions *BootOptions    `json:"boot_options,omitempty"`
}

// SubmenuEntry represents a submenu entry
type SubmenuEntry struct {
	Title       string       `json:"title"`
	Loader      string       `json:"loader,omitempty"`
	Initrd      []string     `json:"initrd,omitempty"`
	Options     string       `json:"options,omitempty"`
	AddOptions  string       `json:"add_options,omitempty"`
	BootOptions *BootOptions `json:"boot_options,omitempty"`
}

// BootOptions represents parsed boot options
type BootOptions struct {
	Root       string `json:"root"`
	RootFlags  string `json:"rootflags"`
	Subvol     string `json:"subvol"`
	SubvolID   string `json:"subvolid"`
	InitrdPath string `json:"initrd_path"`
}
