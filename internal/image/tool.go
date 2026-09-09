// Package image builds and pushes container images through the podman or
// docker CLI. It is independent from the instance backends: a machine with
// kubectl and Podman builds here and runs instances on the cluster.
package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Tool names the CLI that builds and pushes images.
type Tool string

const (
	ToolPodman Tool = "podman"
	ToolDocker Tool = "docker"
	ToolAuto   Tool = "auto"
)

func (t Tool) String() string { return string(t) }

// Known lists the tools devrig can drive, in auto-detection order.
func Known() []string { return []string{ToolPodman.String(), ToolDocker.String()} }

// ParseTool picks a tool from the flag, then the environment, then auto.
func ParseTool(flag, env string) (Tool, error) {
	value := strings.ToLower(strings.TrimSpace(flag))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(env))
	}
	switch value {
	case "", "auto":
		return ToolAuto, nil
	case "podman":
		return ToolPodman, nil
	case "docker":
		return ToolDocker, nil
	default:
		return "", fmt.Errorf("unknown tool %q — use podman, docker or auto", value)
	}
}

// Options configures which CLI to drive.
type Options struct {
	Tool   Tool
	Podman string // binary override, default "podman"
	Docker string // binary override, default "docker"
}

// probeTimeout bounds `<tool> info`, which hangs when a socket is half-alive.
const probeTimeout = 10 * time.Second

type runner interface {
	// run captures output; stdin is written to the process when non-nil.
	run(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
	// stream copies the process output to the writers as it happens.
	stream(ctx context.Context, stdout, stderr io.Writer, args ...string) error
	tool() Tool
}

type execRunner struct {
	kind Tool
	bin  string
}

func (r *execRunner) tool() Tool { return r.kind }

func (r *execRunner) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			return out.Bytes(), fmt.Errorf("%s %s: %w\n%s", r.kind, strings.Join(args, " "), err, msg)
		}
		return out.Bytes(), fmt.Errorf("%s %s: %w", r.kind, strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}

// stream reports only the subcommand on failure: the tool's own stderr has
// already reached the user, and argv may carry --build-arg values.
func (r *execRunner) stream(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w", r.kind, args[0], err)
	}
	return nil
}

// deps are the process-level hooks tests replace.
type deps struct {
	lookPath  func(file string) (string, error)
	newRunner func(tool Tool, path string) runner
}

var defaultDeps = deps{
	lookPath: exec.LookPath,
	newRunner: func(tool Tool, path string) runner {
		return &execRunner{kind: tool, bin: path}
	},
}

// Client drives one image tool.
type Client struct {
	r runner
}

// New finds a working tool: the one asked for, or podman then docker on auto.
func New(ctx context.Context, opt Options) (*Client, error) {
	return newClient(ctx, opt, defaultDeps)
}

func newClient(ctx context.Context, opt Options, d deps) (*Client, error) {
	switch opt.Tool {
	case ToolPodman, ToolDocker:
		r, err := open(ctx, opt.Tool, binaryFor(opt, opt.Tool), d)
		if err != nil {
			return nil, err
		}
		return &Client{r: r}, nil
	}
	var problems []string
	allMissing := true
	for _, tool := range []Tool{ToolPodman, ToolDocker} {
		r, err := open(ctx, tool, binaryFor(opt, tool), d)
		if err == nil {
			return &Client{r: r}, nil
		}
		var notFound *notFoundError
		if errors.As(err, &notFound) {
			problems = append(problems, fmt.Sprintf("%s: %s", tool, err))
			continue
		}
		allMissing = false
		problems = append(problems, fmt.Sprintf("%s: %s", tool, indent(err.Error())))
	}
	hint := "hint: install podman or docker, or pass --tool"
	if !allMissing {
		hint = "hint: pass --tool to pin one"
	}
	return nil, fmt.Errorf("no image tool available\n  %s\n%s",
		strings.Join(problems, "\n  "), hint)
}

// indent nests every line after the first four spaces under the tool name.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func binaryFor(opt Options, tool Tool) string {
	switch tool {
	case ToolPodman:
		if opt.Podman != "" {
			return opt.Podman
		}
	case ToolDocker:
		if opt.Docker != "" {
			return opt.Docker
		}
	}
	return tool.String()
}

// notFoundError says the binary is not on PATH at all, as opposed to found
// but unreachable (a stopped Podman machine, a missing Docker daemon).
type notFoundError struct{ bin string }

func (e *notFoundError) Error() string { return e.bin + " not found in PATH" }

// open resolves the binary and checks it answers: a Podman client with its
// machine stopped or a Docker CLI without a daemon both fail `info`.
func open(ctx context.Context, tool Tool, bin string, d deps) (runner, error) {
	path, err := d.lookPath(bin)
	if err != nil {
		return nil, &notFoundError{bin: bin}
	}
	r := d.newRunner(tool, path)
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if _, err := r.run(pctx, nil, "info"); err != nil {
		return nil, fmt.Errorf("%s not reachable: %w\n%s", tool, err, reachHint(tool))
	}
	return r, nil
}

func reachHint(tool Tool) string {
	switch tool {
	case ToolPodman:
		if runtime.GOOS == "darwin" {
			return "hint: start the VM with `podman machine start`, then check `podman info`"
		}
		return "hint: check `podman info`; rootless Podman needs no service, but this user must be able to run it"
	default:
		if runtime.GOOS == "darwin" {
			return "hint: start Docker Desktop, OrbStack, or Colima; check DOCKER_HOST and `docker info`"
		}
		return "hint: is the Docker daemon running? check /var/run/docker.sock and group membership (`docker`)"
	}
}

// Tool reports which CLI this client drives.
func (c *Client) Tool() Tool { return c.r.tool() }
