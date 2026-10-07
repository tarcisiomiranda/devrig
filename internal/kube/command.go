package kube

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

type commandRunner interface {
	execute(context.Context, rig.CommandOptions, ...string) (int, error)
}

func (r *execRunner) execute(ctx context.Context, opt rig.CommandOptions, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, r.bin, append(r.globalArgs(), args...)...)
	cmd.Stdin = opt.Stdin
	cmd.Stdout, cmd.Stderr = opt.Output()
	err := cmd.Run()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("kubectl exec: %w", err)
	}
	return 0, nil
}

func (c *Client) Exec(ctx context.Context, name string, opt rig.CommandOptions) (*rig.CommandResult, error) {
	if err := opt.Validate(true); err != nil {
		return nil, err
	}
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	if opt.Workdir != "" {
		return nil, fmt.Errorf("exec --workdir is only supported by the Docker backend")
	}
	runner, ok := c.kubectl.(commandRunner)
	if !ok {
		return nil, fmt.Errorf("kubectl runner does not support command streams")
	}
	p, err := c.findPod(ctx, name)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, &rig.NotFoundError{Name: name, Where: "namespace " + c.namespace}
	}
	if !p.running() {
		return nil, fmt.Errorf("instance %q is not running", name)
	}
	st := c.statusFromPod(p, 0)
	env := opt.Environment(c.specOf(p).ClientEnv(st.User, st.Password, instance.DefaultHost, st.ContainerPort, st.Database))
	args := append(namespaceArgs(p.Metadata.Namespace), "exec", "pod/"+p.Metadata.Name, "-c", p.containerName(c.specOf(p).Name.String()))
	if opt.Stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, "--", "env", "--")
	args = append(args, env...)
	args = append(args, opt.Argv...)
	code, err := runner.execute(ctx, opt, args...)
	if err != nil {
		return nil, err
	}
	return &rig.CommandResult{Name: name, Backend: "kube", ExitCode: code}, nil
}

func (c *Client) Run(context.Context, rig.RunOptions) (*rig.CommandResult, error) {
	return nil, fmt.Errorf("run is only supported by the Docker backend; use --backend docker, or exec in an existing Kubernetes instance")
}
