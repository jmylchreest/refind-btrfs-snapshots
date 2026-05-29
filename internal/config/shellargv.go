package config

import (
	"fmt"
	"reflect"

	"github.com/mattn/go-shellwords"
)

// ShellArgv accepts both YAML shapes for command-line argv:
//
//	sign_command: ["peseal", "sign", "{}"]   # list form
//	sign_command: "peseal sign {}"            # string form (shellwords-split)
type ShellArgv []string

func (s ShellArgv) Argv() []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func ParseShellArgv(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	return shellwords.Parse(s)
}

func shellArgvDecodeHook(from, to reflect.Type, data interface{}) (interface{}, error) {
	if to != reflect.TypeOf(ShellArgv{}) || from.Kind() != reflect.String {
		return data, nil
	}
	tokens, err := ParseShellArgv(data.(string))
	if err != nil {
		return nil, fmt.Errorf("sign_command: %w", err)
	}
	return ShellArgv(tokens), nil
}
