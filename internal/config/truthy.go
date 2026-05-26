package config

import (
	"fmt"
	"strings"
)

// Truthy accepts true/false, yes/no, on/off, 1/0, enabled/disabled so
// human-written config files don't fail on common synonyms — yaml.v3 only
// recognises true/false natively. Use IsTrue() in application code.
type Truthy bool

func (t *Truthy) UnmarshalText(text []byte) error {
	s := strings.ToLower(strings.TrimSpace(string(text)))
	switch s {
	case "", "0", "false", "no", "off", "disabled", "disable":
		*t = false
		return nil
	case "1", "true", "yes", "on", "enabled", "enable":
		*t = true
		return nil
	}
	return fmt.Errorf("invalid truthy value %q (want true/false/yes/no/on/off/enabled/disabled)", string(text))
}

func (t Truthy) IsTrue() bool { return bool(t) }
