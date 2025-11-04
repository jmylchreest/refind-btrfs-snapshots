package params

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewParameterParser(t *testing.T) {
	parser := NewParameterParser(",")
	assert.Equal(t, ",", parser.separators)
}

func TestNewSpaceParameterParser(t *testing.T) {
	parser := NewSpaceParameterParser()
	assert.Equal(t, `\s`, parser.separators)
}

func TestNewCommaParameterParser(t *testing.T) {
	parser := NewCommaParameterParser()
	assert.Equal(t, `,\s`, parser.separators)
}

func TestParameterParser_Extract(t *testing.T) {
	tests := []struct {
		name     string
		parser   *ParameterParser
		text     string
		param    string
		expected string
	}{
		{
			name:     "space_separated_basic",
			parser:   NewSpaceParameterParser(),
			text:     "root=UUID=abc123 quiet splash",
			param:    "root",
			expected: "UUID=abc123",
		},
		{
			name:     "comma_separated_basic",
			parser:   NewCommaParameterParser(),
			text:     "subvol=@,compress=zstd",
			param:    "subvol",
			expected: "@",
		},
		{
			name:     "parameter_not_found",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash rw",
			param:    "root",
			expected: "",
		},
		{
			name:     "complex_subvol_path",
			parser:   NewCommaParameterParser(),
			text:     "subvol=/@/.snapshots/123/snapshot,subvolid=456",
			param:    "subvol",
			expected: "/@/.snapshots/123/snapshot",
		},
		{
			name:     "parameter_with_uuid",
			parser:   NewSpaceParameterParser(),
			text:     "root=UUID=12345678-1234-1234-1234-123456789abc quiet",
			param:    "root",
			expected: "UUID=12345678-1234-1234-1234-123456789abc",
		},
		{
			name:     "parameter_at_end",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash rw rootflags=subvol=@",
			param:    "rootflags",
			expected: "subvol=@",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.parser.Extract(tt.text, tt.param)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParameterParser_Update(t *testing.T) {
	tests := []struct {
		name     string
		parser   *ParameterParser
		text     string
		param    string
		newValue string
		expected string
	}{
		{
			name:     "update_existing_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "root=UUID=old-uuid quiet splash",
			param:    "root",
			newValue: "UUID=new-uuid",
			expected: "root=UUID=new-uuid quiet splash",
		},
		{
			name:     "add_new_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash",
			param:    "root",
			newValue: "UUID=abc123",
			expected: "quiet splash root=UUID=abc123",
		},
		{
			name:     "update_subvol_comma_separated",
			parser:   NewCommaParameterParser(),
			text:     "subvol=@,compress=zstd",
			param:    "subvol",
			newValue: "@/.snapshots/123/snapshot",
			expected: "subvol=@/.snapshots/123/snapshot,compress=zstd",
		},
		{
			name:     "update_complex_rootflags",
			parser:   NewSpaceParameterParser(),
			text:     "quiet rootflags=subvol=@ splash",
			param:    "rootflags",
			newValue: "subvol=/@/.snapshots/456/snapshot,subvolid=789",
			expected: "quiet rootflags=subvol=/@/.snapshots/456/snapshot,subvolid=789 splash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.parser.Update(tt.text, tt.param, tt.newValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParameterParser_Has(t *testing.T) {
	tests := []struct {
		name     string
		parser   *ParameterParser
		text     string
		param    string
		expected bool
	}{
		{
			name:     "parameter_exists",
			parser:   NewSpaceParameterParser(),
			text:     "root=UUID=abc123 quiet",
			param:    "root",
			expected: true,
		},
		{
			name:     "parameter_not_exists",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash",
			param:    "root",
			expected: false,
		},
		{
			name:     "comma_separated_exists",
			parser:   NewCommaParameterParser(),
			text:     "subvol=@,compress=zstd",
			param:    "compress",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.parser.Has(tt.text, tt.param)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParameterParser_Remove(t *testing.T) {
	tests := []struct {
		name     string
		parser   *ParameterParser
		text     string
		param    string
		expected string
	}{
		{
			name:     "remove_middle_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet root=UUID=abc123 splash",
			param:    "root",
			expected: "quiet splash",
		},
		{
			name:     "remove_first_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "root=UUID=abc123 quiet splash",
			param:    "root",
			expected: "quiet splash",
		},
		{
			name:     "remove_last_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash root=UUID=abc123",
			param:    "root",
			expected: "quiet splash",
		},
		{
			name:     "remove_nonexistent_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash",
			param:    "root",
			expected: "quiet splash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.parser.Remove(tt.text, tt.param)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBootOptionsParser(t *testing.T) {
	parser := NewBootOptionsParser()
	assert.NotNil(t, parser)
	assert.NotNil(t, parser.SpaceParser)
	assert.NotNil(t, parser.CommaParser)
}

func TestBootOptionsParser_ExtractRootFlags(t *testing.T) {
	parser := NewBootOptionsParser()

	tests := []struct {
		name     string
		options  string
		expected string
	}{
		{
			name:     "basic_rootflags",
			options:  "quiet rootflags=subvol=@ splash",
			expected: "subvol=@",
		},
		{
			name:     "complex_rootflags",
			options:  "quiet rootflags=subvol=/@,compress=zstd,space_cache=v2 splash",
			expected: "subvol=/@,compress=zstd,space_cache=v2",
		},
		{
			name:     "no_rootflags",
			options:  "quiet splash rw",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ExtractRootFlags(tt.options)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBootOptionsParser_ExtractSubvol(t *testing.T) {
	parser := NewBootOptionsParser()

	tests := []struct {
		name      string
		rootflags string
		expected  string
	}{
		{
			name:      "basic_subvol",
			rootflags: "subvol=@",
			expected:  "@",
		},
		{
			name:      "subvol_with_path",
			rootflags: "subvol=/@/.snapshots/123/snapshot",
			expected:  "/@/.snapshots/123/snapshot",
		},
		{
			name:      "subvol_with_other_options",
			rootflags: "subvol=@,compress=zstd",
			expected:  "@",
		},
		{
			name:      "no_subvol",
			rootflags: "compress=zstd,space_cache=v2",
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ExtractSubvol(tt.rootflags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBootOptionsParser_ExtractSubvolID(t *testing.T) {
	parser := NewBootOptionsParser()

	tests := []struct {
		name      string
		rootflags string
		expected  string
	}{
		{
			name:      "basic_subvolid",
			rootflags: "subvolid=123",
			expected:  "123",
		},
		{
			name:      "subvolid_with_other_options",
			rootflags: "subvol=@,subvolid=456,compress=zstd",
			expected:  "456",
		},
		{
			name:      "no_subvolid",
			rootflags: "subvol=@,compress=zstd",
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.ExtractSubvolID(tt.rootflags)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBootOptionsParser_UpdateSubvol(t *testing.T) {
	parser := NewBootOptionsParser()

	tests := []struct {
		name        string
		options     string
		newSubvol   string
		expected    string
		description string
	}{
		{
			name:        "update_existing_subvol",
			options:     "quiet rootflags=subvol=@ splash",
			newSubvol:   "/@/.snapshots/123/snapshot",
			expected:    "quiet rootflags=subvol=/@/.snapshots/123/snapshot splash",
			description: "Should update existing subvol in rootflags",
		},
		{
			name:        "add_subvol_to_existing_rootflags",
			options:     "quiet rootflags=compress=zstd splash",
			newSubvol:   "@/.snapshots/123/snapshot",
			expected:    "quiet rootflags=compress=zstd,subvol=@/.snapshots/123/snapshot splash",
			description: "Should add subvol to existing rootflags",
		},
		{
			name:        "create_rootflags_with_subvol",
			options:     "quiet splash",
			newSubvol:   "@/.snapshots/123/snapshot",
			expected:    "quiet splash rootflags=subvol=@/.snapshots/123/snapshot",
			description: "Should create rootflags with subvol",
		},
		{
			name:        "preserve_format_slash_at",
			options:     "quiet rootflags=subvol=/@ splash",
			newSubvol:   "/@/.snapshots/123/snapshot",
			expected:    "quiet rootflags=subvol=/@/.snapshots/123/snapshot splash",
			description: "Should preserve /@ format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.UpdateSubvol(tt.options, tt.newSubvol)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestBootOptionsParser_UpdateSubvolID(t *testing.T) {
	parser := NewBootOptionsParser()

	tests := []struct {
		name        string
		options     string
		newSubvolID string
		expected    string
		description string
	}{
		{
			name:        "update_existing_subvolid",
			options:     "quiet rootflags=subvol=@,subvolid=123 splash",
			newSubvolID: "456",
			expected:    "quiet rootflags=subvol=@,subvolid=456 splash",
			description: "Should update existing subvolid",
		},
		{
			name:        "add_subvolid_to_existing_rootflags",
			options:     "quiet rootflags=subvol=@ splash",
			newSubvolID: "789",
			expected:    "quiet rootflags=subvol=@,subvolid=789 splash",
			description: "Should add subvolid to existing rootflags",
		},
		{
			name:        "create_rootflags_with_subvolid",
			options:     "quiet splash",
			newSubvolID: "456",
			expected:    "quiet splash rootflags=subvolid=456",
			description: "Should create rootflags with subvolid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.UpdateSubvolID(tt.options, tt.newSubvolID)
			assert.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestParameterParser_ExtractMultiple(t *testing.T) {
	tests := []struct {
		name     string
		parser   *ParameterParser
		text     string
		param    string
		expected []string
	}{
		{
			name:     "multiple_initrd_parameters",
			parser:   NewSpaceParameterParser(),
			text:     "quiet initrd=amd-ucode.img initrd=initramfs-linux-cachyos.img splash",
			param:    "initrd",
			expected: []string{"amd-ucode.img", "initramfs-linux-cachyos.img"},
		},
		{
			name:     "single_initrd_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet initrd=initramfs-linux.img splash",
			param:    "initrd",
			expected: []string{"initramfs-linux.img"},
		},
		{
			name:     "no_initrd_parameters",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash rw",
			param:    "initrd",
			expected: nil,
		},
		{
			name:     "three_initrd_parameters",
			parser:   NewSpaceParameterParser(),
			text:     "initrd=ucode.img initrd=initramfs.img initrd=fallback.img",
			param:    "initrd",
			expected: []string{"ucode.img", "initramfs.img", "fallback.img"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.parser.ExtractMultiple(tt.text, tt.param)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParameterParser_RemoveAll(t *testing.T) {
	tests := []struct {
		name     string
		parser   *ParameterParser
		text     string
		param    string
		expected string
	}{
		{
			name:     "remove_multiple_initrd_parameters",
			parser:   NewSpaceParameterParser(),
			text:     "quiet initrd=amd-ucode.img initrd=initramfs-linux.img splash",
			param:    "initrd",
			expected: "quiet splash",
		},
		{
			name:     "remove_single_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet initrd=initramfs.img splash",
			param:    "initrd",
			expected: "quiet splash",
		},
		{
			name:     "remove_nonexistent_parameter",
			parser:   NewSpaceParameterParser(),
			text:     "quiet splash rw",
			param:    "initrd",
			expected: "quiet splash rw",
		},
		{
			name:     "remove_three_parameters",
			parser:   NewSpaceParameterParser(),
			text:     "root=/dev/sda1 initrd=a.img initrd=b.img initrd=c.img quiet",
			param:    "initrd",
			expected: "root=/dev/sda1 quiet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.parser.RemoveAll(tt.text, tt.param)
			assert.Equal(t, tt.expected, result)
		})
	}
}
