package kube

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/rig"
)

type fakeCommandRunner struct {
	fakeRunner
	command []string
	input   string
	code    int
}

func (r *fakeCommandRunner) execute(_ context.Context, opt rig.CommandOptions, args ...string) (int, error) {
	r.command = args
	if opt.Stdin != nil {
		input, err := io.ReadAll(opt.Stdin)
		if err != nil {
			return 0, err
		}
		r.input = string(input)
	}
	stdout, stderr := opt.Output()
	if _, err := io.WriteString(stdout, "out\n"); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(stderr, "err\n"); err != nil {
		return 0, err
	}
	return r.code, nil
}

func TestExecTargetsLabelledPodWithExactArgumentsAndStdin(t *testing.T) {
	runner := &fakeCommandRunner{fakeRunner: fakeRunner{out: map[string][]byte{"get pods": []byte(`{"items":[` + runningPodJSON + `]}`)}}, code: 9}
	c := testClient(t, runner)
	var stdout, stderr bytes.Buffer
	argv := []string{"psql", "-c", "SELECT $$'x' $HOME;\n日本語$$;", ""}
	result, err := c.Exec(context.Background(), "app", rig.CommandOptions{
		Argv: argv, Env: map[string]string{"VALUE": "a'b\n$c", "PGDATABASE": "override"},
		Stdin: strings.NewReader("'\"\n$"), Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 9 || stdout.String() != "out\n" || stderr.String() != "err\n" || runner.input != "'\"\n$" {
		t.Fatalf("result = %+v, stdout = %q, stderr = %q, input = %q", result, stdout.String(), stderr.String(), runner.input)
	}
	if !reflect.DeepEqual(runner.command[len(runner.command)-len(argv):], argv) {
		t.Fatalf("argv changed: %q", runner.command)
	}
	joined := strings.Join(runner.command, " ")
	if !strings.Contains(joined, "-i -- env --") || !strings.Contains(joined, "PGHOST=127.0.0.1") || !strings.Contains(joined, "PGDATABASE=override") || !strings.Contains(strings.Join(runner.seen[0], " "), "devrig.managed=1") {
		t.Fatalf("missing stream, connection or ownership options: %q", runner.command)
	}
}

func TestKubeCommandLimitationsFailBeforeExecution(t *testing.T) {
	runner := &fakeCommandRunner{}
	c := testClient(t, runner)
	if _, err := c.Exec(context.Background(), "app", rig.CommandOptions{Argv: []string{"pwd"}, Workdir: "/tmp"}); err == nil || len(runner.seen) > 0 {
		t.Fatalf("workdir must fail explicitly: %v", err)
	}
	if _, err := c.Run(context.Background(), rig.RunOptions{Image: "helper"}); err == nil || !strings.Contains(err.Error(), "only supported") || len(runner.seen) > 0 {
		t.Fatalf("run must fail explicitly: %v", err)
	}
}
