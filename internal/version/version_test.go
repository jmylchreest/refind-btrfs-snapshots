package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestString(t *testing.T) {
	tests := []struct {
		name         string
		versionValue string
		expected     string
	}{
		{"version_set", "1.2.3", "1.2.3"},
		{"version_empty", "", "dev"},
		{"version_dev", "dev", "dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalVersion := Version
			Version = tt.versionValue
			defer func() { Version = originalVersion }()

			assert.Equal(t, tt.expected, String())
		})
	}
}
