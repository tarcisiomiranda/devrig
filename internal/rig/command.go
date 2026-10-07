package rig

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

type CommandOptions struct {
	Argv    []string
	Env     map[string]string
	Workdir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

type RunOptions struct {
	CommandOptions
	Name    string
	Image   string
	Connect string
}

type CommandResult struct {
	Name         string `json:"name"`
	Backend      string `json:"backend"`
	ContainerID  string `json:"container_id,omitempty"`
	Image        string `json:"image,omitempty"`
	ExitCode     int    `json:"exit_code"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	StdoutBase64 string `json:"stdout_base64,omitempty"`
	StderrBase64 string `json:"stderr_base64,omitempty"`
	StdoutFile   string `json:"stdout_file,omitempty"`
	StderrFile   string `json:"stderr_file,omitempty"`
}

type CommandBackend interface {
	Exec(context.Context, string, CommandOptions) (*CommandResult, error)
	Run(context.Context, RunOptions) (*CommandResult, error)
}

type CommandStepResult struct {
	*CommandResult
	Line  int    `json:"line"`
	Error string `json:"error,omitempty"`
}

type CommandBatchResult struct {
	ExitCode int                 `json:"exit_code"`
	Total    int                 `json:"total"`
	Results  []CommandStepResult `json:"results"`
	Error    string              `json:"error,omitempty"`
}

type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("command exited with code %d", e.Code)
}

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (o CommandOptions) Validate(requireCommand bool) error {
	if (requireCommand && len(o.Argv) == 0) || (len(o.Argv) > 0 && o.Argv[0] == "") {
		return fmt.Errorf("command is required: use -- <command> [args...] or argv in --request")
	}
	for _, arg := range o.Argv {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command arguments cannot contain NUL bytes")
		}
	}
	if strings.ContainsRune(o.Workdir, 0) {
		return fmt.Errorf("workdir cannot contain NUL bytes")
	}
	for key, value := range o.Env {
		if !envKey.MatchString(key) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("environment requires valid variable names and values without NUL bytes")
		}
	}
	return nil
}

func (o CommandOptions) Environment(defaults map[string]string) []string {
	merged := make(map[string]string, len(defaults)+len(o.Env))
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range o.Env {
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+merged[key])
	}
	return env
}

func (o CommandOptions) Output() (io.Writer, io.Writer) {
	stdout, stderr := o.Stdout, o.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return stdout, stderr
}
