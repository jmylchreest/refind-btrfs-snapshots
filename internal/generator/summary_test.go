package generator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogSummary_DoesNotPanic(t *testing.T) {
	summary := &OperationSummary{
		IncludedSnapshots: []string{"snapshot1"},
		AddedSnapshots:    []string{"snapshot1"},
		RemovedSnapshots:  []string{},
		UpdatedFstabs:     []string{"/fstab"},
		UpdatedConfigs:    []string{"/config"},
		WritableChanges:   []string{"change1"},
	}

	assert.NotPanics(t, func() { LogSummary(summary, true) })
	assert.NotPanics(t, func() { LogSummary(summary, false) })
}
