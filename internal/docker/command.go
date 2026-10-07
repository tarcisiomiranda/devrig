package docker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

func (c *Client) commandTarget(ctx context.Context, name string) (*container.Summary, map[string]string, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, nil, err
	}
	target, err := c.findByName(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if target == nil {
		return nil, nil, &rig.NotFoundError{Name: name, Where: "docker"}
	}
	insp, err := c.cli.ContainerInspect(ctx, target.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect command target: %w", err)
	}
	if insp.State == nil || !insp.State.Running {
		return nil, nil, fmt.Errorf("instance %q is not running", name)
	}
	if target.Labels[instance.LabelKind] == "run" {
		return target, nil, nil
	}
	st, err := c.statusFromSummary(ctx, target, false)
	if err != nil {
		return nil, nil, err
	}
	spec, err := engine.Lookup(st.Engine)
	if err != nil {
		return nil, nil, err
	}
	return target, spec.ClientEnv(st.User, st.Password, instance.DefaultHost, st.ContainerPort, st.Database), nil
}

func (c *Client) Exec(ctx context.Context, name string, opt rig.CommandOptions) (*rig.CommandResult, error) {
	if err := opt.Validate(true); err != nil {
		return nil, err
	}
	target, env, err := c.commandTarget(ctx, name)
	if err != nil {
		return nil, err
	}
	created, err := c.cli.ContainerExecCreate(ctx, target.ID, container.ExecOptions{
		Cmd: opt.Argv, Env: opt.Environment(env), WorkingDir: opt.Workdir,
		AttachStdin: opt.Stdin != nil, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create exec: %w", err)
	}
	attached, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("attach exec: %w", err)
	}
	defer attached.Close()
	if err := commandStreams(ctx, attached, opt); err != nil {
		return nil, err
	}
	for {
		state, err := c.cli.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return nil, fmt.Errorf("inspect exec: %w", err)
		}
		if !state.Running {
			return &rig.CommandResult{Name: name, Backend: "docker", ContainerID: shortID(target.ID), ExitCode: state.ExitCode}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (c *Client) Run(ctx context.Context, opt rig.RunOptions) (result *rig.CommandResult, err error) {
	if err := opt.Validate(false); err != nil {
		return nil, err
	}
	if strings.TrimSpace(opt.Image) == "" {
		return nil, fmt.Errorf("image is required")
	}
	if opt.Name == "" {
		id := make([]byte, 8)
		if _, err := rand.Read(id); err != nil {
			return nil, fmt.Errorf("generate run name: %w", err)
		}
		opt.Name = fmt.Sprintf("run-%x", id)
	}
	if err := instance.ValidateName(opt.Name); err != nil {
		return nil, err
	}
	host := &container.HostConfig{}
	var env map[string]string
	if opt.Connect != "" {
		target, defaults, err := c.commandTarget(ctx, opt.Connect)
		if err != nil {
			return nil, err
		}
		host.NetworkMode = container.NetworkMode("container:" + target.ID)
		env = defaults
	}
	if err := c.ensureImage(ctx, opt.Image); err != nil {
		return nil, err
	}
	cfg := &container.Config{
		Image: opt.Image, Env: opt.Environment(env), WorkingDir: opt.Workdir,
		AttachStdout: true, AttachStderr: true,
		AttachStdin: opt.Stdin != nil, OpenStdin: opt.Stdin != nil, StdinOnce: opt.Stdin != nil,
		Labels: map[string]string{
			instance.LabelManaged: "1", instance.LabelName: opt.Name,
			instance.LabelImage: opt.Image, instance.LabelKind: "run",
		},
	}
	if len(opt.Argv) > 0 {
		cfg.Entrypoint = opt.Argv[:1]
		cfg.Cmd = opt.Argv[1:]
	}
	created, err := c.cli.ContainerCreate(ctx, cfg, host, nil, nil, instance.ContainerName(opt.Name))
	if err != nil {
		return nil, fmt.Errorf("create run container: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err = errors.Join(err, c.removeContainer(cleanup, created.ID))
	}()
	attached, err := c.cli.ContainerAttach(ctx, created.ID, container.AttachOptions{
		Stream: true, Stdin: opt.Stdin != nil, Stdout: true, Stderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("attach run container: %w", err)
	}
	defer attached.Close()
	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("start run container: %w", err)
	}
	if err := commandStreams(ctx, attached, opt.CommandOptions); err != nil {
		return nil, err
	}
	states, failures := c.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-failures:
		return nil, fmt.Errorf("wait run container: %w", err)
	case state := <-states:
		if state.Error != nil {
			return nil, fmt.Errorf("run container: %s", state.Error.Message)
		}
		return &rig.CommandResult{Name: opt.Name, Backend: "docker", ContainerID: shortID(created.ID), Image: opt.Image, ExitCode: int(state.StatusCode)}, nil
	}
}

func commandStreams(ctx context.Context, attached types.HijackedResponse, opt rig.CommandOptions) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-streamCtx.Done()
		attached.Close()
	}()
	inputErrors := make(chan error, 1)
	if opt.Stdin != nil {
		go func() {
			_, err := io.Copy(attached.Conn, opt.Stdin)
			if err != nil {
				inputErrors <- err
				attached.CloseWrite()
				return
			}
			err = attached.CloseWrite()
			inputErrors <- err
			if err != nil {
				attached.Close()
			}
		}()
	}
	stdout, stderr := opt.Output()
	_, err := stdcopy.StdCopy(stdout, stderr, attached.Reader)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case inputErr := <-inputErrors:
		if inputErr != nil {
			return fmt.Errorf("write command input: %w", inputErr)
		}
	default:
	}
	if err != nil {
		return fmt.Errorf("read command output: %w", err)
	}
	return nil
}
