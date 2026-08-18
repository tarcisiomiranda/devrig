package docker

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/wait"
)

// Client wraps the Docker Engine API for devrig instances.
type Client struct {
	cli *client.Client
}

// New creates a Docker client from the environment (DOCKER_HOST, etc.).
func New(ctx context.Context) (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w\n%s", err, dockerHint())
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker not reachable: %w\n%s", err, dockerHint())
	}
	return &Client{cli: cli}, nil
}

func (c *Client) Close() error {
	if c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

func dockerHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "hint: start Docker Desktop, OrbStack, or Colima; check DOCKER_HOST and `docker info`"
	case "linux":
		return "hint: is the Docker daemon running? check /var/run/docker.sock and group membership (`docker`)"
	default:
		return "hint: Docker Engine must be running and reachable"
	}
}

// Status is the public JSON shape for an instance.
type Status struct {
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	ContainerID   string `json:"container_id,omitempty"`
	ContainerName string `json:"container_name"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	Host          string `json:"host"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user"`
	Password      string `json:"password"`
	Database      string `json:"database"`
	Image         string `json:"image"`
	URL           string `json:"url,omitempty"`
}

// DownResult is the JSON shape for down.
type DownResult struct {
	Name    string `json:"name"`
	Removed bool   `json:"removed"`
	State   string `json:"state"`
}

// LogsResult is the JSON shape for logs.
type LogsResult struct {
	Name string `json:"name"`
	Logs string `json:"logs"`
}

// UpOptions configures a new instance.
type UpOptions struct {
	Name     string
	Engine   *engine.Spec
	User     string
	Password string
	Database string
	Image    string
	Port     int // 0 = ephemeral
	Timeout  time.Duration
}

// Up creates or reuses a ready database instance.
func (c *Client) Up(ctx context.Context, opt UpOptions) (*Status, error) {
	if err := instance.ValidateName(opt.Name); err != nil {
		return nil, err
	}
	if opt.Engine == nil {
		opt.Engine = engine.Default()
	}
	if opt.User == "" {
		opt.User = opt.Engine.DefaultUser
	}
	if opt.Password == "" {
		opt.Password = opt.Engine.DefaultPassword
	}
	if opt.Database == "" {
		opt.Database = instance.DefaultDatabase(opt.Name)
	}
	if opt.Image == "" {
		opt.Image = opt.Engine.DefaultImage
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}

	existing, err := c.findByName(ctx, opt.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		st, err := c.statusFromSummary(ctx, existing, true)
		if err == nil && st.Ready && st.State == "running" {
			if st.User == opt.User && st.Password == opt.Password &&
				st.Database == opt.Database && st.Image == opt.Image &&
				(opt.Port == 0 || st.Port == opt.Port) {
				return st, nil
			}
		}
		if err := c.removeContainer(ctx, existing.ID); err != nil {
			return nil, err
		}
	}

	if err := c.ensureImage(ctx, opt.Image); err != nil {
		return nil, err
	}

	cname := instance.ContainerName(opt.Name)
	labels := map[string]string{
		instance.LabelManaged: "1",
		instance.LabelName:    opt.Name,
		instance.LabelDB:      opt.Database,
		instance.LabelUser:    opt.User,
		instance.LabelImage:   opt.Image,
		instance.LabelPass:    opt.Password,
		instance.LabelEngine:  opt.Engine.Name.String(),
	}

	hostPort := ""
	if opt.Port > 0 {
		hostPort = strconv.Itoa(opt.Port)
	}

	containerPort := nat.Port(strconv.Itoa(opt.Engine.ContainerPort) + "/tcp")
	cfg := &container.Config{
		Image:  opt.Image,
		Env:    opt.Engine.Env(opt.User, opt.Password, opt.Database),
		Labels: labels,
		ExposedPorts: nat.PortSet{
			containerPort: struct{}{},
		},
	}
	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			containerPort: []nat.PortBinding{
				{HostIP: "127.0.0.1", HostPort: hostPort},
			},
		},
	}

	create, err := c.cli.ContainerCreate(ctx, cfg, hostCfg, &network.NetworkingConfig{}, nil, cname)
	if err != nil {
		if strings.Contains(err.Error(), "Conflict") || strings.Contains(err.Error(), "already in use") {
			return nil, fmt.Errorf("container name %q already in use by a non-devrig container; rename or remove it manually", cname)
		}
		return nil, fmt.Errorf("container create: %w", err)
	}

	if err := c.cli.ContainerStart(ctx, create.ID, container.StartOptions{}); err != nil {
		_ = c.removeContainer(ctx, create.ID)
		return nil, fmt.Errorf("container start: %w", err)
	}

	wctx, cancel := context.WithTimeout(ctx, opt.Timeout)
	defer cancel()

	st, err := c.waitReady(wctx, create.ID, opt)
	if err != nil {
		_ = c.removeContainer(context.Background(), create.ID)
		return nil, err
	}
	return st, nil
}

func (c *Client) waitReady(ctx context.Context, id string, opt UpOptions) (*Status, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("database did not become ready within %s: %w", opt.Timeout, err)
		}
		insp, err := c.cli.ContainerInspect(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("inspect: %w", err)
		}
		if !insp.State.Running {
			if insp.State.Status == "exited" || insp.State.Status == "dead" {
				return nil, fmt.Errorf("container exited before ready (status=%s)", insp.State.Status)
			}
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("database did not become ready within %s", opt.Timeout)
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		port, err := publishedPort(insp)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("database did not become ready within %s: %w", opt.Timeout, err)
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}
		url := instance.URL(opt.Engine, opt.User, opt.Password, instance.DefaultHost, port, opt.Database)
		if err := wait.Ready(ctx, opt.Engine, url, instance.DefaultHost, port); err != nil {
			return nil, err
		}
		return &Status{
			Name:          opt.Name,
			Engine:        opt.Engine.Name.String(),
			ContainerID:   shortID(id),
			ContainerName: strings.TrimPrefix(insp.Name, "/"),
			State:         "running",
			Ready:         true,
			Host:          instance.DefaultHost,
			Port:          port,
			User:          opt.User,
			Password:      opt.Password,
			Database:      opt.Database,
			Image:         opt.Image,
			URL:           url,
		}, nil
	}
}

// Down stops and removes the instance. Missing is success.
func (c *Client) Down(ctx context.Context, name string) (*DownResult, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	existing, err := c.findByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return &DownResult{Name: name, Removed: false, State: "absent"}, nil
	}
	if err := c.removeContainer(ctx, existing.ID); err != nil {
		return nil, err
	}
	return &DownResult{Name: name, Removed: true, State: "absent"}, nil
}

// Status returns the current instance status.
func (c *Client) Status(ctx context.Context, name string) (*Status, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	existing, err := c.findByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("instance %q not found", name)
	}
	return c.statusFromSummary(ctx, existing, true)
}

// List returns all managed instances.
func (c *Client) List(ctx context.Context) ([]Status, error) {
	list, err := c.listManaged(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(list))
	for i := range list {
		st, err := c.statusFromSummary(ctx, &list[i], true)
		if err != nil {
			continue
		}
		out = append(out, *st)
	}
	return out, nil
}

// URL returns the connection URL if ready.
func (c *Client) URL(ctx context.Context, name string) (string, error) {
	st, err := c.Status(ctx, name)
	if err != nil {
		return "", err
	}
	if !st.Ready || st.URL == "" {
		return "", fmt.Errorf("instance %q is not ready (state=%s)", name, st.State)
	}
	return st.URL, nil
}

// Logs returns container logs.
func (c *Client) Logs(ctx context.Context, name string, tail int) (*LogsResult, error) {
	if err := instance.ValidateName(name); err != nil {
		return nil, err
	}
	existing, err := c.findByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("instance %q not found", name)
	}
	if tail <= 0 {
		tail = 100
	}
	rc, err := c.cli.ContainerLogs(ctx, existing.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return nil, fmt.Errorf("logs: %w", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return &LogsResult{Name: name, Logs: stripDockerLogHeaders(b)}, nil
}

// Docker ANDs filter terms, so current and legacy labels need separate queries.
func (c *Client) listManaged(ctx context.Context) ([]container.Summary, error) {
	seen := make(map[string]bool)
	var all []container.Summary
	for _, label := range []string{
		instance.LabelManaged + "=1",
		instance.LegacyLabelManaged + "=1",
	} {
		f := filters.NewArgs()
		f.Add("label", label)
		list, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
		if err != nil {
			return nil, fmt.Errorf("list: %w", err)
		}
		for i := range list {
			if !seen[list[i].ID] {
				seen[list[i].ID] = true
				all = append(all, list[i])
			}
		}
	}
	return all, nil
}

func (c *Client) findByName(ctx context.Context, name string) (*container.Summary, error) {
	var list []container.Summary
	for _, pair := range [][2]string{
		{instance.LabelManaged + "=1", instance.LabelName + "=" + name},
		{instance.LegacyLabelManaged + "=1", instance.LegacyLabelName + "=" + name},
	} {
		f := filters.NewArgs()
		f.Add("label", pair[0])
		f.Add("label", pair[1])
		found, err := c.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
		if err != nil {
			return nil, fmt.Errorf("list by name: %w", err)
		}
		list = append(list, found...)
	}
	if len(list) == 0 {
		return nil, nil
	}
	for i := range list {
		if list[i].State == "running" {
			return &list[i], nil
		}
	}
	return &list[0], nil
}

func label(sum *container.Summary, key, legacy string) string {
	if v := sum.Labels[key]; v != "" {
		return v
	}
	return sum.Labels[legacy]
}

func (c *Client) statusFromSummary(ctx context.Context, sum *container.Summary, probeReady bool) (*Status, error) {
	name := label(sum, instance.LabelName, instance.LegacyLabelName)
	spec := engine.Default()
	if e := sum.Labels[instance.LabelEngine]; e != "" {
		if s, err := engine.Lookup(e); err == nil {
			spec = s
		}
	}
	user := label(sum, instance.LabelUser, instance.LegacyLabelUser)
	if user == "" {
		user = spec.DefaultUser
	}
	pass := label(sum, instance.LabelPass, instance.LegacyLabelPass)
	if pass == "" {
		pass = spec.DefaultPassword
	}
	db := label(sum, instance.LabelDB, instance.LegacyLabelDB)
	if db == "" {
		db = instance.DefaultDatabase(name)
	}
	img := label(sum, instance.LabelImage, instance.LegacyLabelImage)
	if img == "" {
		img = sum.Image
	}

	st := &Status{
		Name:          name,
		Engine:        spec.Name.String(),
		ContainerID:   shortID(sum.ID),
		ContainerName: instance.ContainerName(name),
		State:         sum.State,
		User:          user,
		Password:      pass,
		Database:      db,
		Image:         img,
		Host:          instance.DefaultHost,
	}
	if len(sum.Names) > 0 {
		st.ContainerName = strings.TrimPrefix(sum.Names[0], "/")
	}

	insp, err := c.cli.ContainerInspect(ctx, sum.ID)
	if err != nil {
		return st, nil
	}
	st.State = insp.State.Status
	if port, err := publishedPort(insp); err == nil {
		st.Port = port
		st.URL = instance.URL(spec, user, pass, instance.DefaultHost, port, db)
		if probeReady && insp.State.Running {
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := wait.Ready(pctx, spec, st.URL, instance.DefaultHost, port); err == nil {
				st.Ready = true
			}
		}
	}
	return st, nil
}

func (c *Client) removeContainer(ctx context.Context, id string) error {
	err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("remove: %w", err)
	}
	return nil
}

func (c *Client) ensureImage(ctx context.Context, ref string) error {
	_, err := c.cli.ImageInspect(ctx, ref)
	if err == nil {
		return nil
	}
	rc, err := c.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("image pull %s: %w", ref, err)
	}
	defer rc.Close()
	_, _ = io.Copy(io.Discard, rc)
	if _, err := c.cli.ImageInspect(ctx, ref); err != nil {
		return fmt.Errorf("image %s not available after pull: %w", ref, err)
	}
	return nil
}

func publishedPort(insp container.InspectResponse) (int, error) {
	if insp.NetworkSettings == nil {
		return 0, fmt.Errorf("no network settings")
	}
	bindings, ok := insp.NetworkSettings.Ports["5432/tcp"]
	if !ok || len(bindings) == 0 {
		return 0, fmt.Errorf("port 5432/tcp not published")
	}
	for _, b := range bindings {
		if b.HostIP == "" || b.HostIP == "0.0.0.0" || b.HostIP == "127.0.0.1" || b.HostIP == "::" {
			p, err := strconv.Atoi(b.HostPort)
			if err != nil {
				continue
			}
			return p, nil
		}
	}
	p, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return 0, err
	}
	return p, nil
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func stripDockerLogHeaders(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var out strings.Builder
	i := 0
	for i+8 <= len(b) {
		size := int(b[i+4])<<24 | int(b[i+5])<<16 | int(b[i+6])<<8 | int(b[i+7])
		i += 8
		if size < 0 || i+size > len(b) {
			return string(b)
		}
		out.Write(b[i : i+size])
		i += size
	}
	if out.Len() == 0 {
		return string(b)
	}
	return out.String()
}
