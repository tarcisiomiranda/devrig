//go:build integration

package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

func TestCommandLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	suffix := fmt.Sprint(time.Now().UnixNano())
	sourceName, targetName := "command-source-"+suffix, "command-target-"+suffix
	for _, name := range []string{sourceName, targetName} {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := c.Down(cleanup, name); err != nil {
				t.Errorf("cleanup %s: %v", name, err)
			}
		})
	}
	source, err := c.Up(ctx, rig.UpOptions{Name: sourceName, Database: "command_source", Engine: engine.Default()})
	if err != nil {
		t.Fatal(err)
	}
	target, err := c.Up(ctx, rig.UpOptions{Name: targetName, Database: "command_target", Engine: engine.Default()})
	if err != nil {
		t.Fatal(err)
	}
	payload := "quotes ' \" $HOME $(touch /devrig-must-not-exist); `echo x` \\ tabs\t\ná日本語"
	var stdout, stderr bytes.Buffer
	run := func(name string, argv []string, input *bytes.Reader) *rig.CommandResult {
		t.Helper()
		stdout.Reset()
		stderr.Reset()
		opt := rig.CommandOptions{Argv: argv, Stdout: &stdout, Stderr: &stderr}
		if input != nil {
			opt.Stdin = input
		}
		result, err := c.Exec(ctx, name, opt)
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("exec: result = %+v, err = %v, stderr = %q", result, err, stderr.String())
		}
		return result
	}
	run(sourceName, []string{"printf", "%s", payload}, nil)
	if stdout.String() != payload {
		t.Fatalf("argument changed: %q", stdout.String())
	}
	sql := "CREATE TABLE sample (value text); INSERT INTO sample VALUES ($devrig$" + payload + "$devrig$);"
	run(sourceName, []string{"psql", "-v", "ON_ERROR_STOP=1"}, bytes.NewReader([]byte(sql)))
	stdout.Reset()
	stderr.Reset()
	dumpResult, err := c.Run(ctx, rig.RunOptions{
		Name: "command-dump-" + suffix, Image: source.Image, Connect: sourceName,
		CommandOptions: rig.CommandOptions{Argv: []string{"pg_dump", "--format=custom", "--no-owner", "--no-acl"}, Stdout: &stdout, Stderr: &stderr},
	})
	if err != nil || dumpResult.ExitCode != 0 || !bytes.HasPrefix(stdout.Bytes(), []byte("PGDMP")) {
		t.Fatalf("dump: result = %+v, err = %v, stderr = %q", dumpResult, err, stderr.String())
	}
	dump := bytes.Clone(stdout.Bytes())
	stdout.Reset()
	stderr.Reset()
	restoreResult, err := c.Run(ctx, rig.RunOptions{
		Name: "command-restore-" + suffix, Image: source.Image, Connect: targetName,
		CommandOptions: rig.CommandOptions{
			Argv:  []string{"pg_restore", "--exit-on-error", "--no-owner", "--no-acl", "--dbname", target.Database},
			Stdin: bytes.NewReader(dump), Stdout: &stdout, Stderr: &stderr,
		},
	})
	if err != nil || restoreResult.ExitCode != 0 {
		t.Fatalf("restore: result = %+v, err = %v, stderr = %q", restoreResult, err, stderr.String())
	}
	run(targetName, []string{"psql", "-At", "-c", "SELECT value FROM sample"}, nil)
	if stdout.String() != payload+"\n" {
		t.Fatalf("restored payload changed: %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	failure, err := c.Run(ctx, rig.RunOptions{
		Name: "command-failure-" + suffix, Image: source.Image,
		CommandOptions: rig.CommandOptions{Argv: []string{"sh", "-c", "printf partial; printf diagnostic >&2; exit 23"}, Stdout: &stdout, Stderr: &stderr},
	})
	if err != nil || failure.ExitCode != 23 || stdout.String() != "partial" || stderr.String() != "diagnostic" {
		t.Fatalf("failure: result = %+v, err = %v, stdout = %q, stderr = %q", failure, err, stdout.String(), stderr.String())
	}
	timeout, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	_, err = c.Run(timeout, rig.RunOptions{
		Name: "command-timeout-" + suffix, Image: source.Image, CommandOptions: rig.CommandOptions{Argv: []string{"sleep", "300"}},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
	items, err := c.listManaged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Labels[instance.LabelKind] == "run" && strings.HasSuffix(item.Labels[instance.LabelName], suffix) {
			t.Fatalf("helper survived cleanup: %s", item.ID)
		}
	}
}
