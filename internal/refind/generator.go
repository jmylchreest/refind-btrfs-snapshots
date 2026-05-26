package refind

import (
	"github.com/jmylchreest/refind-btrfs-snapshots/internal/kernel"
)

// Generator handles rEFInd config generation
type Generator struct {
	parser       *Parser
	espPath      string
	bootSets     []*kernel.BootSet
	bootPlans    []*kernel.BootPlan
	menuFormat   string
	useLocalTime bool
}

// NewGenerator creates a new rEFInd config generator.
// menuFormat is the time.Format layout used for snapshot display names;
// useLocalTime renders timestamps in local time instead of UTC.
func NewGenerator(espPath, menuFormat string, useLocalTime bool) *Generator {
	return &Generator{
		parser:       NewParser(espPath),
		espPath:      espPath,
		menuFormat:   menuFormat,
		useLocalTime: useLocalTime,
	}
}

// NewGeneratorWithBootPlans creates a new rEFInd config generator with detected boot sets
// and per-snapshot boot plans. Boot plans enable btrfs-mode submenu generation where
// kernels are loaded from inside the snapshot rather than the ESP.
func NewGeneratorWithBootPlans(espPath, menuFormat string, useLocalTime bool, scanner *kernel.Scanner, bootSets []*kernel.BootSet, bootPlans []*kernel.BootPlan) *Generator {
	return &Generator{
		parser:       NewParserWithScanner(espPath, scanner),
		espPath:      espPath,
		bootSets:     bootSets,
		bootPlans:    bootPlans,
		menuFormat:   menuFormat,
		useLocalTime: useLocalTime,
	}
}
