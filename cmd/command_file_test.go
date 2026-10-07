package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/rig"
)

func TestCommandTextPreservesQuotedAndEscapedArguments(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{`printf '%s' '' "" 'a b' a\ b`, []string{"printf", "%s", "", "", "a b", "a b"}},
		{"echo '$HOME' \"$HOME\" '`literal`' '$(literal)' '*.sql'", []string{"echo", "$HOME", "$HOME", "`literal`", "$(literal)", "*.sql"}},
		{`psql -c "SELECT 'a;|&<> b', $$'quoted'$$;"`, []string{"psql", "-c", "SELECT 'a;|&<> b', $$'quoted'$$;"}},
		{`echo "a\"b" 'c\d' "e\f" g\\h "\$HOME"`, []string{"echo", `a"b`, `c\d`, `e\f`, `g\h`, "$HOME"}},
		{`echo pre' with space'" and more"post`, []string{"echo", "pre with space and morepost"}},
		{`echo 'á日本語' 'hash#part' \#literal tail#part # ignored`, []string{"echo", "á日本語", "hash#part", "#literal", "tail#part"}},
		{" \t # ignored", nil},
		{`echo \; \| \& \< \>`, []string{"echo", ";", "|", "&", "<", ">"}},
	} {
		got, err := splitCommandLine(tc.line)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("split(%q) = %q, err = %v, want %q", tc.line, got, err, tc.want)
		}
	}
}

func TestCommandTextRejectsMalformedQuotesAndImplicitShellOperators(t *testing.T) {
	for _, line := range []string{
		`echo "unfinished`, `echo 'unfinished`, "echo unfinished\\", `echo x; echo y`, `echo x && echo y`,
		`cat < file`, `echo x > file`, `echo x | cat`, "echo \xff",
	} {
		if _, err := splitCommandLine(line); err == nil {
			t.Fatalf("accepted unsupported line %q", line)
		}
	}
}

func executeBatchCommand(t *testing.T, run bool, backend *commandStub, input string, args ...string) (rig.CommandBatchResult, error) {
	t.Helper()
	cmd := newCommand(run, func(context.Context) (rig.Backend, error) { return backend, nil })
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(args)
	err := cmd.Execute()
	var batch rig.CommandBatchResult
	if output.Len() > 0 {
		if decodeErr := json.Unmarshal(output.Bytes(), &batch); decodeErr != nil {
			t.Fatalf("invalid batch JSON %q: %v", output.String(), decodeErr)
		}
	}
	return batch, err
}

func TestCommandBatchKeepsOrderPhysicalLinesAndSeparateOutput(t *testing.T) {
	backend := &commandStub{stubBackend: stubBackend{kind: rig.KindDocker}, stdout: []byte("out"), stderr: "err"}
	input := "\ufeff# comment\r\n\r\nprintf '%s' 'a b'\r\n# comment\r\necho \"$HOME\" ''"
	batch, err := executeBatchCommand(t, false, backend, input, "app", "--commands-file", "-")
	if err != nil || batch.ExitCode != 0 || batch.Total != 2 || len(batch.Results) != 2 {
		t.Fatalf("batch = %+v, err = %v", batch, err)
	}
	if !reflect.DeepEqual(backend.history, [][]string{{"printf", "%s", "a b"}, {"echo", "$HOME", ""}}) {
		t.Fatalf("commands changed: %q", backend.history)
	}
	if batch.Results[0].Line != 3 || batch.Results[1].Line != 5 {
		t.Fatalf("physical line numbers were lost: %+v", batch.Results)
	}
	for _, result := range batch.Results {
		if result.Stdout != "out" || result.Stderr != "err" {
			t.Fatalf("output from other commands leaked: %+v", result)
		}
	}
}

func TestCommandBatchStopsOnFirstCommandFailure(t *testing.T) {
	backend := &commandStub{codes: []int{0, 7, 0}, stdout: []byte("partial")}
	batch, err := executeBatchCommand(t, true, backend, "first\nsecond\nthird\n", "helper-image", "--commands-file", "-", "--connect", "app", "--name", "helper")
	if ExitCode(err) != 7 || batch.ExitCode != 7 || batch.Total != 3 || len(batch.Results) != 2 || backend.calls != 2 {
		t.Fatalf("batch = %+v, err = %v, calls = %d", batch, err, backend.calls)
	}
	if batch.Results[1].ExitCode != 7 || batch.Results[1].Stdout != "partial" || backend.runOpt.Connect != "app" || backend.runOpt.Name != "helper" {
		t.Fatalf("failed result or helper options lost: batch = %+v, run = %+v", batch, backend.runOpt)
	}
}

func TestCommandBatchKeepsEarlierResultsOnInfrastructureFailure(t *testing.T) {
	backend := &commandStub{failAt: 2, stdout: []byte("completed"), stubBackend: stubBackend{err: errors.New("backend failed")}}
	batch, err := executeBatchCommand(t, false, backend, "first\nsecond\nthird\n", "app", "--commands-file", "-")
	if err == nil || batch.ExitCode != 1 || backend.calls != 2 || len(batch.Results) != 2 || batch.Results[0].Stdout != "completed" || batch.Results[1].Error != "backend failed" {
		t.Fatalf("batch = %+v, err = %v, calls = %d", batch, err, backend.calls)
	}
}

func TestCommandBatchValidatesEntireFileBeforeOpeningBackend(t *testing.T) {
	for _, input := range []string{"valid-command\necho 'unclosed\n", "echo \x00\n", "\n# comments only\n", "''\n"} {
		calls := 0
		cmd := newCommand(false, func(context.Context) (rig.Backend, error) {
			calls++
			return &commandStub{}, nil
		})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs([]string{"app", "--commands-file", "-"})
		if err := cmd.Execute(); err == nil || calls != 0 {
			t.Fatalf("preflight failed: err = %v, backend calls = %d", err, calls)
		}
	}
}

func TestCommandBatchReplaysStdinFileAndAppendsOutputBytes(t *testing.T) {
	dir := t.TempDir()
	commands, input, output := filepath.Join(dir, "commands.txt"), filepath.Join(dir, "input.dump"), filepath.Join(dir, "output.dump")
	if err := os.WriteFile(commands, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte{0, 0xff, '\n', 0xfe}
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	batch, err := executeBatchCommand(t, false, &commandStub{}, "", "app", "--commands-file", commands, "--stdin-file", input, "--stdout-file", output)
	if err != nil || len(batch.Results) != 2 {
		t.Fatalf("batch = %+v, err = %v", batch, err)
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, append(bytes.Clone(data), data...)) {
		t.Fatalf("output bytes = %v, err = %v", got, err)
	}
	for _, result := range batch.Results {
		if result.Stdout != "" || result.StdoutFile != output {
			t.Fatalf("file result = %+v", result)
		}
	}
}

func TestCommandBatchRejectsConflictingModes(t *testing.T) {
	for _, args := range [][]string{
		{"app", "--commands-file", "-", "--request", "-"},
		{"app", "--commands-file", "-", "--", "echo"},
		{"app", "--commands-file", "-", "-i"},
		{"app", "--commands-file", "-", "--stdin-file", "-"},
	} {
		backend := &commandStub{}
		if _, err := executeBatchCommand(t, false, backend, "echo value\n", args...); err == nil || backend.calls != 0 {
			t.Fatalf("conflicting modes accepted: args = %v, err = %v", args, err)
		}
	}
}

func TestCommandBatchAcceptsLongLines(t *testing.T) {
	value := strings.Repeat("x", 128*1024)
	commands, err := readCommandLines("-", strings.NewReader("echo '"+value+"'\n"))
	if err != nil || len(commands) != 1 || commands[0].argv[1] != value {
		t.Fatalf("long line was truncated: err = %v", err)
	}
}
