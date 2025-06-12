package runner

import (
	"os"
	"os/exec"

	"github.com/rs/zerolog/log"
)

// Runner defines the interface for executing operations
type Runner interface {
	Command(name string, args []string, description string) error
	WriteFile(path string, content []byte, perm os.FileMode, description string) error
	MkdirAll(path string, perm os.FileMode, description string) error
	IsDryRun() bool
}

// RealRunner executes operations for real
type RealRunner struct{}

func (r *RealRunner) Command(name string, args []string, description string) error {
	log.Debug().
		Str("command", name+" "+joinArgs(args)).
		Str("description", description).
		Msg("Executing command")

	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func (r *RealRunner) WriteFile(path string, content []byte, perm os.FileMode, description string) error {
	log.Debug().
		Str("path", path).
		Str("description", description).
		Int("size", len(content)).
		Msg("Writing file")

	return os.WriteFile(path, content, perm)
}

func (r *RealRunner) MkdirAll(path string, perm os.FileMode, description string) error {
	log.Debug().
		Str("path", path).
		Str("description", description).
		Msg("Creating directory")

	return os.MkdirAll(path, perm)
}

func (r *RealRunner) IsDryRun() bool {
	return false
}

// DryRunner logs operations without executing them
type DryRunner struct{}

func (r *DryRunner) Command(name string, args []string, description string) error {
	log.Info().
		Str("command", name+" "+joinArgs(args)).
		Str("description", description).
		Msg("[DRY RUN] Would execute command")
	return nil
}

func (r *DryRunner) WriteFile(path string, content []byte, perm os.FileMode, description string) error {
	log.Info().
		Str("path", path).
		Str("description", description).
		Int("size", len(content)).
		Msg("[DRY RUN] Would write file")
	return nil
}

func (r *DryRunner) MkdirAll(path string, perm os.FileMode, description string) error {
	log.Info().
		Str("path", path).
		Str("description", description).
		Msg("[DRY RUN] Would create directory")
	return nil
}

func (r *DryRunner) IsDryRun() bool {
	return true
}

// New creates the appropriate runner based on dry-run mode
func New(dryRun bool) Runner {
	if dryRun {
		return &DryRunner{}
	}
	return &RealRunner{}
}

func joinArgs(args []string) string {
	result := ""
	for i, arg := range args {
		if i > 0 {
			result += " "
		}
		result += arg
	}
	return result
}
