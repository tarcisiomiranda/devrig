package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

type commandRequest struct {
	Argv       []string          `json:"argv"`
	Env        map[string]string `json:"env"`
	Workdir    string            `json:"workdir"`
	Stdin      *string           `json:"stdin"`
	StdinFile  string            `json:"stdin_file"`
	StdoutFile string            `json:"stdout_file"`
	StderrFile string            `json:"stderr_file"`
	Name       string            `json:"name"`
	Connect    string            `json:"connect"`
}

type commandFlags struct {
	request      string
	commandsFile string
	stdinFile    string
	stdoutFile   string
	stderrFile   string
	workdir      string
	env          []string
	name         string
	connect      string
	interactive  bool
	timeout      time.Duration
}

func newCommand(run bool, selectBackend func(context.Context) (rig.Backend, error)) *cobra.Command {
	var flags commandFlags
	use, short := "exec <name> [flags] -- <command> [args...]", "Execute a command in a managed instance (JSON)"
	if run {
		use, short = "run <image> [flags] -- <command> [args...]", "Run a disposable helper container and remove it (Docker, JSON)"
	}
	cmd := &cobra.Command{
		Use: use, Short: short,
		Long: short + ".\n\nArguments are passed directly, without a shell. Use --request FILE for a JSON\nobject with argv, env, stdin or stdin_file; --request - reads JSON from stdin.\nUse --commands-file FILE for one command per text line, stopping at the first\nfailure. stdout and stderr are returned separately, with exit_code. Use\n--stdout-file for binary or large output. Output files are never overwritten.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			request, err := flags.load(cmd, args[1:], run)
			if err != nil {
				return err
			}
			var lines []commandLine
			if flags.commandsFile != "" {
				lines, err = readCommandLines(flags.commandsFile, cmd.InOrStdin())
				if err != nil {
					return err
				}
			}
			if !run {
				if err := instance.ValidateName(args[0]); err != nil {
					return err
				}
			}
			for _, name := range []string{request.Name, request.Connect} {
				if name != "" {
					if err := instance.ValidateName(name); err != nil {
						return err
					}
				}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if flags.timeout < 0 {
				return fmt.Errorf("timeout must not be negative")
			}
			if flags.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, flags.timeout)
				defer cancel()
			}
			stdout, err := newCommandOutput(request.StdoutFile)
			if err != nil {
				return err
			}
			defer stdout.close()
			stderr, err := newCommandOutput(request.StderrFile)
			if err != nil {
				return err
			}
			defer stderr.close()
			backend, err := selectBackend(ctx)
			if err != nil {
				return err
			}
			defer backend.Close()
			commands, ok := backend.(rig.CommandBackend)
			if !ok {
				return fmt.Errorf("backend %s does not support commands", backend.Kind())
			}
			if len(lines) > 0 {
				return executeCommandBatch(ctx, cmd, commands, args[0], run, request, lines, stdout, stderr)
			}
			result, err := executeCommandRequest(ctx, commands, args[0], run, request, flags.interactive, cmd.InOrStdin(), stdout, stderr)
			if err != nil {
				return err
			}
			if err := errors.Join(stdout.close(), stderr.close()); err != nil {
				return fmt.Errorf("close command output: %w", err)
			}
			if err := encodeCommandResult(cmd.OutOrStdout(), result); err != nil {
				return err
			}
			if result.ExitCode != 0 {
				return &rig.ExitError{Code: result.ExitCode}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&flags.request, "request", "", "JSON request file; - reads the request from stdin")
	f.StringVar(&flags.commandsFile, "commands-file", "", "text file with one command per line; stop on first failure; - reads stdin")
	f.StringVar(&flags.stdinFile, "stdin-file", "", "read command stdin from a file; - reads stdin")
	f.StringVar(&flags.stdoutFile, "stdout-file", "", "write command stdout bytes to a new file")
	f.StringVar(&flags.stderrFile, "stderr-file", "", "write command stderr bytes to a new file")
	f.StringVar(&flags.workdir, "workdir", "", "working directory inside the container (Docker only)")
	f.StringArrayVarP(&flags.env, "env", "e", nil, "environment override KEY=VALUE (repeatable)")
	f.BoolVarP(&flags.interactive, "interactive", "i", false, "read command stdin from the calling process; no TTY")
	f.DurationVar(&flags.timeout, "timeout", 0, "command time limit (0 = no limit)")
	if run {
		f.StringVar(&flags.name, "name", "", "helper instance name (default: unique generated name)")
		f.StringVar(&flags.connect, "connect", "", "share a managed instance's network and client connection environment")
	}
	return cmd
}

func (f commandFlags) load(cmd *cobra.Command, argv []string, run bool) (*commandRequest, error) {
	if f.commandsFile != "" && (f.request != "" || len(argv) > 0) {
		return nil, fmt.Errorf("--commands-file cannot be combined with --request or command arguments")
	}
	if f.commandsFile != "" && (f.interactive || f.stdinFile == "-") {
		return nil, fmt.Errorf("--commands-file requires replayable stdin: use --stdin-file FILE instead of -i or --stdin-file -")
	}
	request := &commandRequest{}
	if f.request != "" {
		reader := cmd.InOrStdin()
		if f.request != "-" {
			file, err := os.Open(f.request)
			if err != nil {
				return nil, fmt.Errorf("open command request: %w", err)
			}
			defer file.Close()
			reader = file
		}
		dec := json.NewDecoder(reader)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil {
			return nil, fmt.Errorf("decode command request: %w", err)
		}
		if request == nil {
			return nil, fmt.Errorf("command request must be a JSON object")
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("command request must contain exactly one JSON object")
		}
		if f.request != "-" {
			for _, path := range []*string{&request.StdinFile, &request.StdoutFile, &request.StderrFile} {
				if *path != "" && !(path == &request.StdinFile && *path == "-") && !filepath.IsAbs(*path) {
					*path = filepath.Join(filepath.Dir(f.request), *path)
				}
			}
		}
	}
	if len(argv) > 0 {
		if len(request.Argv) > 0 {
			return nil, fmt.Errorf("use command arguments or request argv, not both")
		}
		request.Argv = argv
	}
	if request.Env == nil {
		request.Env = make(map[string]string)
	}
	for _, assignment := range f.env {
		key, value, found := strings.Cut(assignment, "=")
		if !found {
			return nil, fmt.Errorf("--env requires KEY=VALUE")
		}
		request.Env[key] = value
	}
	for _, override := range []struct {
		flag, value string
		target      *string
	}{
		{"stdin-file", f.stdinFile, &request.StdinFile},
		{"stdout-file", f.stdoutFile, &request.StdoutFile},
		{"stderr-file", f.stderrFile, &request.StderrFile},
		{"workdir", f.workdir, &request.Workdir},
		{"name", f.name, &request.Name},
		{"connect", f.connect, &request.Connect},
	} {
		if cmd.Flags().Changed(override.flag) {
			*override.target = override.value
		}
	}
	if !run && (request.Name != "" || request.Connect != "") {
		return nil, fmt.Errorf("name and connect in a request are only supported by run")
	}
	if err := (rig.CommandOptions{Argv: request.Argv, Env: request.Env, Workdir: request.Workdir}).Validate(!run && f.commandsFile == ""); err != nil {
		return nil, err
	}
	inputSources := 0
	if request.Stdin != nil {
		inputSources++
	}
	if request.StdinFile != "" {
		inputSources++
	}
	if f.interactive {
		inputSources++
	}
	if inputSources > 1 {
		return nil, fmt.Errorf("choose one stdin source: request stdin, stdin_file/--stdin-file, or -i")
	}
	if f.request == "-" && (f.interactive || request.StdinFile == "-") {
		return nil, fmt.Errorf("stdin cannot carry both the JSON request and command input; use request stdin or a file")
	}
	return request, nil
}

func commandInput(request *commandRequest, interactive bool, stdin io.Reader) (io.Reader, func(), error) {
	close := func() {}
	if request.Stdin != nil {
		return strings.NewReader(*request.Stdin), close, nil
	}
	if interactive || request.StdinFile == "-" {
		return stdin, close, nil
	}
	if request.StdinFile != "" {
		file, err := os.Open(request.StdinFile)
		if err != nil {
			return nil, close, fmt.Errorf("open command stdin: %w", err)
		}
		return file, func() { file.Close() }, nil
	}
	return nil, close, nil
}

type commandOutput struct {
	writer io.Writer
	buffer bytes.Buffer
	file   *os.File
}

func newCommandOutput(path string) (*commandOutput, error) {
	output := &commandOutput{}
	output.writer = &output.buffer
	if path != "" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create command output file: %w", err)
		}
		output.file, output.writer = file, file
	}
	return output, nil
}

func (o *commandOutput) close() error {
	if o.file == nil {
		return nil
	}
	err := o.file.Close()
	o.file = nil
	return err
}

func (o *commandOutput) text() (string, string) {
	if !utf8.Valid(o.buffer.Bytes()) {
		return "", base64.StdEncoding.EncodeToString(o.buffer.Bytes())
	}
	return o.buffer.String(), ""
}

func encodeCommandResult(writer io.Writer, result any) error {
	enc := json.NewEncoder(writer)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("encode command result: %w", err)
	}
	return nil
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *rig.ExitError
	if errors.As(err, &exit) && exit.Code > 0 && exit.Code <= 255 {
		return exit.Code
	}
	return 1
}
