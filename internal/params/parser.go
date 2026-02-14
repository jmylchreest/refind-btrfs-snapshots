package params

import (
	"fmt"
	"regexp"
	"strings"
)

// whitespaceRun matches one or more whitespace characters, used for collapsing runs.
var whitespaceRun = regexp.MustCompile(`\s+`)

// ParameterParser handles parameter extraction and manipulation from option strings
type ParameterParser struct {
	separators string
}

// NewParameterParser creates a new parameter parser with the given separators
func NewParameterParser(separators string) *ParameterParser {
	return &ParameterParser{separators: separators}
}

// NewSpaceParameterParser creates a parser for space-separated parameters
func NewSpaceParameterParser() *ParameterParser {
	return &ParameterParser{separators: `\s`}
}

// NewCommaParameterParser creates a parser for comma and space-separated parameters
func NewCommaParameterParser() *ParameterParser {
	return &ParameterParser{separators: `,\s`}
}

// Extract extracts the value of a parameter from the given text
func (p *ParameterParser) Extract(text, param string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`%s=([^%s]+)`,
		regexp.QuoteMeta(param), p.separators))
	matches := pattern.FindStringSubmatch(text)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// Update replaces the value of a parameter in the given text
func (p *ParameterParser) Update(text, param, newValue string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`%s=([^%s]+)`,
		regexp.QuoteMeta(param), p.separators))

	replacement := fmt.Sprintf("%s=%s", param, newValue)

	if pattern.MatchString(text) {
		return pattern.ReplaceAllString(text, replacement)
	}

	// Parameter doesn't exist, append it
	if text == "" {
		return replacement
	}

	// Use appropriate separator based on parser type
	separator := " "
	if strings.Contains(p.separators, ",") {
		separator = ","
	}
	return text + separator + replacement
}

// Has checks if a parameter exists in the text
func (p *ParameterParser) Has(text, param string) bool {
	pattern := regexp.MustCompile(fmt.Sprintf(`%s=`, regexp.QuoteMeta(param)))
	return pattern.MatchString(text)
}

// Remove removes a parameter from the text
func (p *ParameterParser) Remove(text, param string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`\s*%s=([^%s]+)\s*`,
		regexp.QuoteMeta(param), p.separators))
	return strings.TrimSpace(pattern.ReplaceAllString(text, " "))
}

// ExtractAll extracts all parameter key-value pairs from the text
func (p *ParameterParser) ExtractAll(text string) map[string]string {
	params := make(map[string]string)
	pattern := regexp.MustCompile(`(\w+)=([^` + p.separators + `]+)`)
	matches := pattern.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			params[match[1]] = match[2]
		}
	}

	return params
}

// ExtractMultiple extracts all values for a parameter that may appear multiple times
func (p *ParameterParser) ExtractMultiple(text, param string) []string {
	var values []string
	pattern := regexp.MustCompile(fmt.Sprintf(`%s=([^%s]+)`,
		regexp.QuoteMeta(param), p.separators))
	matches := pattern.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, match[1])
		}
	}

	return values
}

// RemoveAll removes all instances of a parameter from the text
func (p *ParameterParser) RemoveAll(text, param string) string {
	pattern := regexp.MustCompile(fmt.Sprintf(`\s*%s=([^%s]+)`,
		regexp.QuoteMeta(param), p.separators))
	result := pattern.ReplaceAllString(text, "")
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(result, " "))
}

// BootOptionsParser provides specialized parsing for boot options
type BootOptionsParser struct {
	SpaceParser *ParameterParser
	CommaParser *ParameterParser
}

// NewBootOptionsParser creates a new boot options parser
func NewBootOptionsParser() *BootOptionsParser {
	return &BootOptionsParser{
		SpaceParser: NewSpaceParameterParser(),
		CommaParser: NewCommaParameterParser(),
	}
}

// ExtractRootFlags extracts the rootflags parameter from boot options
func (p *BootOptionsParser) ExtractRootFlags(options string) string {
	return p.SpaceParser.Extract(options, "rootflags")
}

// ExtractSubvol extracts the subvol parameter from rootflags
func (p *BootOptionsParser) ExtractSubvol(rootflags string) string {
	return p.CommaParser.Extract(rootflags, "subvol")
}

// ExtractSubvolID extracts the subvolid parameter from rootflags
func (p *BootOptionsParser) ExtractSubvolID(rootflags string) string {
	return p.CommaParser.Extract(rootflags, "subvolid")
}

// UpdateSubvol updates the subvol parameter in boot options
func (p *BootOptionsParser) UpdateSubvol(options, newSubvol string) string {
	rootflags := p.ExtractRootFlags(options)
	if rootflags == "" {
		// No rootflags, add them
		newRootflags := fmt.Sprintf("subvol=%s", newSubvol)
		return p.SpaceParser.Update(options, "rootflags", newRootflags)
	}

	// Update subvol in existing rootflags
	updatedRootflags := p.CommaParser.Update(rootflags, "subvol", newSubvol)
	return p.SpaceParser.Update(options, "rootflags", updatedRootflags)
}

// UpdateSubvolID updates the subvolid parameter in boot options
func (p *BootOptionsParser) UpdateSubvolID(options, newSubvolID string) string {
	rootflags := p.ExtractRootFlags(options)
	if rootflags == "" {
		// No rootflags, add them
		newRootflags := fmt.Sprintf("subvolid=%s", newSubvolID)
		return p.SpaceParser.Update(options, "rootflags", newRootflags)
	}

	// Update subvolid in existing rootflags
	updatedRootflags := p.CommaParser.Update(rootflags, "subvolid", newSubvolID)
	return p.SpaceParser.Update(options, "rootflags", updatedRootflags)
}
