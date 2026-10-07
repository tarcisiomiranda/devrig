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
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/rig"
)

type commandStub struct {
	stubBackend
	options rig.CommandOptions
	runOpt  rig.RunOptions
	code    int
	stdout  []byte
	stderr  string
	calls   int
	codes   []int
	history [][]string
	failAt  int
}

func (s *commandStub) Exec(_ context.Context, name string, opt rig.CommandOptions) (*rig.CommandResult, error) {
	s.calls++
	s.history = append(s.history, append([]string(nil), opt.Argv...))
	s.options = opt
	if s.err != nil && (s.failAt == 0 || s.failAt == s.calls) {
		return nil, s.err
	}
	if opt.Stdin != nil {
		if _, err := io.Copy(opt.Stdout, opt.Stdin); err != nil {
			return nil, err
		}
	}
	if _, err := opt.Stdout.Write(s.stdout); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(opt.Stderr, s.stderr); err != nil {
		return nil, err
	}
	code := s.code
	if s.calls <= len(s.codes) {
		code = s.codes[s.calls-1]
	}
	return &rig.CommandResult{Name: name, Backend: s.kind.String(), ExitCode: code}, nil
}

func (s *commandStub) Run(ctx context.Context, opt rig.RunOptions) (*rig.CommandResult, error) {
	s.runOpt = opt
	return s.Exec(ctx, opt.Name, opt.CommandOptions)
}

func executeCommand(t *testing.T, run bool, backend *commandStub, input string, args ...string) (rig.CommandResult, string, error) {
	t.Helper()
	command := newCommand(run, func(context.Context) (rig.Backend, error) { return backend, nil })
	command.SilenceErrors, command.SilenceUsage = true, true
	var output, stderr bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&stderr)
	command.SetIn(strings.NewReader(input))
	command.SetArgs(args)
	err := command.Execute()
	var result rig.CommandResult
	if output.Len() > 0 {
		if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
			t.Fatalf("invalid JSON result %q: %v", output.String(), decodeErr)
		}
	}
	return result, stderr.String(), err
}

func TestCommandRequestPreservesArgumentsAndInput(t *testing.T) {
	argv := []string{"psql", "", "a b", "'$HOME' \"quoted\" ; $(touch /should-not-exist)", "first\nsecond", "á日本語", "a\\b", "-c", "SELECT $$x'y$$;"}
	input := "'\"$`\\\ná\t<&>\u0000"
	request, err := json.Marshal(commandRequest{Argv: argv, Env: map[string]string{"VALUE": input[:len(input)-1]}, Stdin: &input})
	if err != nil {
		t.Fatal(err)
	}
	backend := &commandStub{stubBackend: stubBackend{kind: rig.KindDocker}, stderr: "diagnostic\n"}
	result, _, err := executeCommand(t, false, backend, string(request), "app", "--request", "-")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(backend.options.Argv, argv) || backend.options.Env["VALUE"] != input[:len(input)-1] {
		t.Fatalf("arguments or environment changed: %+v", backend.options)
	}
	if result.Stdout != input || result.Stderr != "diagnostic\n" || result.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestCommandExitCodeKeepsJSONOutput(t *testing.T) {
	backend := &commandStub{stubBackend: stubBackend{kind: rig.KindDocker}, code: 7, stdout: []byte("partial output"), stderr: "failed"}
	result, stderr, err := executeCommand(t, false, backend, "", "app", "--", "exit-seven")
	if ExitCode(err) != 7 || result.ExitCode != 7 || result.Stdout != "partial output" || result.Stderr != "failed" || stderr != "" {
		t.Fatalf("result = %+v, stderr = %q, err = %v", result, stderr, err)
	}
}

func TestCommandRequestRejectsAmbiguousOrInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name, request string
		args          []string
	}{
		{"unknown field", `{"argv":["cat"],"stdiin":"lost"}`, nil},
		{"multiple objects", `{"argv":["cat"]} {}`, nil},
		{"null", `null`, nil},
		{"NUL argument", `{"argv":["echo","a\u0000b"]}`, nil},
		{"NUL environment", `{"argv":["env"],"env":{"VALUE":"a\u0000b"}}`, nil},
		{"invalid environment key", `{"argv":["env"],"env":{"BAD=KEY":"secret"}}`, nil},
		{"two argv sources", `{"argv":["cat"]}`, []string{"--", "cat"}},
		{"two stdin sources", `{"argv":["cat"],"stdin":"one","stdin_file":"two"}`, nil},
		{"request and stdin", `{"argv":["cat"]}`, []string{"-i"}},
		{"exec connect", `{"argv":["cat"],"connect":"other"}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &commandStub{}
			args := append([]string{"app", "--request", "-"}, tc.args...)
			_, _, err := executeCommand(t, false, backend, tc.request, args...)
			if err == nil || backend.calls != 0 {
				t.Fatalf("err = %v, command calls = %d", err, backend.calls)
			}
		})
	}
}

func TestRunRequestUsesPathsRelativeToRequestAndStreamsBinary(t *testing.T) {
	dir := t.TempDir()
	input := []byte{0, 1, 0xff, '\n', 0xfe}
	if err := os.WriteFile(filepath.Join(dir, "input.dump"), input, 0o600); err != nil {
		t.Fatal(err)
	}
	request := `{"argv":["pg_restore","--list"],"stdin_file":"input.dump","stdout_file":"output.dump","stderr_file":"error.log","name":"validate","connect":"database"}`
	path := filepath.Join(dir, "request.json")
	if err := os.WriteFile(path, []byte(request), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &commandStub{stubBackend: stubBackend{kind: rig.KindDocker}, stderr: "details"}
	result, _, err := executeCommand(t, true, backend, "", "backup-image", "--request", path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(result.StdoutFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, input) || result.Stdout != "" || backend.runOpt.Image != "backup-image" || backend.runOpt.Connect != "database" || backend.runOpt.Name != "validate" {
		t.Fatalf("output = %v, result = %+v, run = %+v", output, result, backend.runOpt)
	}
	if info, err := os.Stat(result.StdoutFile); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("output permissions: info = %v, err = %v", info, err)
	}
	_, _, err = executeCommand(t, true, backend, "", "backup-image", "--request", path)
	if err == nil {
		t.Fatal("existing output must not be overwritten")
	}
	output, _ = os.ReadFile(result.StdoutFile)
	if !bytes.Equal(output, input) {
		t.Fatal("existing output changed")
	}
}

func TestCommandBinaryOutputUsesBase64(t *testing.T) {
	output := []byte{0, 0xff, 0xfe}
	backend := &commandStub{stdout: output}
	result, _, err := executeCommand(t, false, backend, "", "app", "--", "binary-output")
	if err != nil || result.Stdout != "" || result.StdoutBase64 != base64.StdEncoding.EncodeToString(output) {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
}

func TestCommandFlagsPreserveCommasEqualsAndOverrideRequest(t *testing.T) {
	backend := &commandStub{}
	request := `{"argv":["echo","from request"],"env":{"VALUE":"old"},"workdir":"/old"}`
	_, _, err := executeCommand(t, false, backend, request, "app", "--request", "-", "--env", "VALUE=a,b=c", "--workdir", "/with space")
	if err != nil || backend.options.Env["VALUE"] != "a,b=c" || backend.options.Workdir != "/with space" {
		t.Fatalf("options = %+v, err = %v", backend.options, err)
	}
}

func TestAutoCommandsUseFallbackOnlyForMissingTargets(t *testing.T) {
	missing := &commandStub{stubBackend: stubBackend{kind: rig.KindDocker, err: &rig.NotFoundError{Name: "app"}}}
	found := &commandStub{stubBackend: stubBackend{kind: rig.KindKube}}
	opt := rig.CommandOptions{Argv: []string{"echo"}, Stdout: io.Discard, Stderr: io.Discard}
	result, err := auto(missing, found).Exec(context.Background(), "app", opt)
	if err != nil || result.Backend != "kube" || found.calls != 1 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	missing.err = errors.New("permission denied")
	if _, err := auto(missing, found).Exec(context.Background(), "app", opt); err == nil || found.calls != 1 {
		t.Fatalf("must preserve primary failure: err = %v, fallback calls = %d", err, found.calls)
	}
	missing.err = &rig.NotFoundError{Name: "app"}
	if _, err := auto(missing, found).Run(context.Background(), rig.RunOptions{CommandOptions: opt, Image: "image", Connect: "app"}); err != nil || found.calls != 2 {
		t.Fatalf("connected run fallback: err = %v, fallback calls = %d", err, found.calls)
	}
	if _, err := auto(missing, found).Run(context.Background(), rig.RunOptions{CommandOptions: opt, Image: "image"}); err == nil || found.calls != 2 {
		t.Fatalf("unconnected run must stay on primary: err = %v, fallback calls = %d", err, found.calls)
	}
}

func TestExitCodeForInfrastructureFailure(t *testing.T) {
	if ExitCode(nil) != 0 || ExitCode(fmt.Errorf("failed")) != 1 || ExitCode(&rig.ExitError{Code: 300}) != 1 {
		t.Fatal("invalid exit code mapping")
	}
}
