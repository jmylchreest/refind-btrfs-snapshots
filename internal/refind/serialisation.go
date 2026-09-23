package refind

import (
	"errors"
	"strings"
)

// configSerialiser encodes rEFInd strings, not shell/Go strings. Both managed
// stanzas and refind_linux.conf use these rules: double embedded quotes and
// leave backslashes literal. Line breaks and NUL cannot be represented.
// https://www.rodsbooks.com/refind/configfile.html (options)
// https://www.rodsbooks.com/refind/linux.html (refind_linux.conf)
// Keep this format-specific codec here; BLS and UKI do not use rEFInd quoting.
type configSerialiser struct {
	strings.Builder
	err error
}

func (s *configSerialiser) quote(value string) string {
	if strings.ContainsAny(value, "\r\n\x00") && s.err == nil {
		s.err = errors.New("rEFInd configuration value contains a line break or NUL")
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// token keeps simple paths readable, quoting anything the tokenizer would split
// or treat as a comment. Options and titles always use quote instead.
func (s *configSerialiser) token(value string) string {
	if value == "" || strings.ContainsAny(value, " \t=,#\"\r\n\x00") {
		return s.quote(value)
	}
	return value
}

func (s *configSerialiser) result() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.String(), nil
}

// configTokens decodes rEFInd's ReadTokenLine syntax: outside quotes, spaces,
// tabs, commas and '=' separate tokens and '#' starts a comment. Doubled quotes
// represent one literal quote; backslashes are not escape characters.
// Unlike firmware, we retain '/' in unquoted paths for host filesystem access.
func configTokens(line string) []string {
	var tokens []string
	for i := 0; i < len(line); {
		for i < len(line) && strings.ContainsRune(" \t=,", rune(line[i])) {
			i++
		}
		if i == len(line) || line[i] == '#' {
			break
		}
		quoted := line[i] == '"'
		if quoted {
			i++
		}
		var value strings.Builder
		for i < len(line) {
			c := line[i]
			if c == '"' {
				if i+1 < len(line) && line[i+1] == '"' {
					value.WriteByte('"')
					i += 2
					continue
				}
				i++
				break
			}
			if !quoted && strings.ContainsRune(" \t=,#", rune(c)) {
				break
			}
			value.WriteByte(c)
			i++
		}
		tokens = append(tokens, value.String())
	}
	return tokens
}

// directiveValue accepts legacy unquoted values emitted by older versions so
// regeneration repairs them. Quoted values are decoded once before rewriting.
func directiveValue(line string) (string, string) {
	line = strings.TrimSpace(line)
	key, value, _ := strings.Cut(line, " ")
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		key = line[:i]
		value = strings.TrimLeft(line[i:], " \t=")
	}
	if strings.HasPrefix(value, `"`) {
		tokens := configTokens(value)
		if len(tokens) > 0 {
			return strings.ToLower(key), tokens[0]
		}
	}
	value, _, _ = strings.Cut(value, "#")
	return strings.ToLower(key), strings.TrimSpace(value)
}
