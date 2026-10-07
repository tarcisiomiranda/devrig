package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

type commandLine struct {
	line int
	argv []string
}

func readCommandLines(path string, stdin io.Reader) ([]commandLine, error) {
	reader := stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open commands file: %w", err)
		}
		defer file.Close()
		reader = file
	}
	input := bufio.NewReader(reader)
	var commands []commandLine
	for line := 1; ; line++ {
		text, readErr := input.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("read commands file: %w", readErr)
		}
		if line == 1 {
			text = strings.TrimPrefix(text, "\ufeff")
		}
		text = strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
		argv, err := splitCommandLine(text)
		if err == nil && len(argv) > 0 {
			err = (rig.CommandOptions{Argv: argv}).Validate(true)
		}
		if err != nil {
			return nil, fmt.Errorf("commands file line %d: %w", line, err)
		}
		if len(argv) > 0 {
			commands = append(commands, commandLine{line: line, argv: argv})
		}
		if readErr == io.EOF {
			break
		}
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("commands file contains no commands")
	}
	return commands, nil
}

func splitCommandLine(line string) ([]string, error) {
	if !utf8.ValidString(line) {
		return nil, fmt.Errorf("commands file must be UTF-8")
	}
	var argv []string
	var word strings.Builder
	var quote byte
	started := false
	for i := 0; i < len(line); i++ {
		char := line[i]
		if char == '\\' && quote != '\'' {
			if i+1 == len(line) {
				return nil, fmt.Errorf("unfinished escape; each command must fit on one line")
			}
			next := line[i+1]
			if quote == 0 || strings.ContainsRune("$`\"\\", rune(next)) {
				i++
				char = next
			}
			word.WriteByte(char)
			started = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteByte(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote, started = char, true
		case ' ', '\t', '\r':
			if started {
				argv = append(argv, word.String())
				word.Reset()
				started = false
			}
		case '#':
			if !started {
				i = len(line)
			} else {
				word.WriteByte(char)
			}
		case ';', '|', '&', '<', '>':
			return nil, fmt.Errorf("shell operators require an explicitly invoked shell; use one command per line")
		default:
			word.WriteByte(char)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote; each command must fit on one line")
	}
	if started {
		argv = append(argv, word.String())
	}
	return argv, nil
}

func executeCommandRequest(ctx context.Context, commands rig.CommandBackend, target string, run bool, request *commandRequest, interactive bool, stdin io.Reader, stdout, stderr *commandOutput) (*rig.CommandResult, error) {
	input, closeInput, err := commandInput(request, interactive, stdin)
	if err != nil {
		return nil, err
	}
	defer closeInput()
	opt := rig.CommandOptions{Argv: request.Argv, Env: request.Env, Workdir: request.Workdir, Stdin: input, Stdout: stdout.writer, Stderr: stderr.writer}
	var result *rig.CommandResult
	if run {
		result, err = commands.Run(ctx, rig.RunOptions{CommandOptions: opt, Image: target, Name: request.Name, Connect: request.Connect})
	} else {
		result, err = commands.Exec(ctx, target, opt)
	}
	if result != nil {
		setCommandOutput(result, request, stdout, stderr)
	}
	return result, err
}

func setCommandOutput(result *rig.CommandResult, request *commandRequest, stdout, stderr *commandOutput) {
	result.Stdout, result.StdoutBase64 = stdout.text()
	result.Stderr, result.StderrBase64 = stderr.text()
	result.StdoutFile, result.StderrFile = request.StdoutFile, request.StderrFile
}

func executeCommandBatch(ctx context.Context, cmd *cobra.Command, commands rig.CommandBackend, target string, run bool, request *commandRequest, lines []commandLine, stdout, stderr *commandOutput) error {
	batch := rig.CommandBatchResult{Total: len(lines), Results: make([]rig.CommandStepResult, 0, len(lines))}
	var failure error
	for _, line := range lines {
		stdout.buffer.Reset()
		stderr.buffer.Reset()
		request.Argv = line.argv
		result, err := executeCommandRequest(ctx, commands, target, run, request, false, cmd.InOrStdin(), stdout, stderr)
		step := rig.CommandStepResult{Line: line.line, CommandResult: result}
		if err != nil {
			if result == nil {
				result = &rig.CommandResult{ExitCode: 1}
				setCommandOutput(result, request, stdout, stderr)
				step.CommandResult = result
			}
			step.Error = err.Error()
			failure = err
			batch.ExitCode = 1
		} else if result.ExitCode != 0 {
			batch.ExitCode = result.ExitCode
			failure = &rig.ExitError{Code: result.ExitCode}
		}
		batch.Results = append(batch.Results, step)
		if failure != nil {
			break
		}
	}
	if err := errors.Join(stdout.close(), stderr.close()); err != nil {
		batch.ExitCode, batch.Error = 1, "close command output: "+err.Error()
		failure = fmt.Errorf("close command output: %w", err)
	}
	if err := encodeCommandResult(cmd.OutOrStdout(), batch); err != nil {
		return err
	}
	return failure
}
