package image

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// BuildOptions mirrors the subset of `build` flags devrig exposes.
type BuildOptions struct {
	Context   string   // build context, "." when empty
	File      string   // -f; empty lets the tool pick Containerfile/Dockerfile
	Tags      []string // -t, at least one
	Platform  string   // --platform, one platform
	BuildArgs []string // --build-arg KEY=VALUE, passed through
	Target    string   // --target
	NoCache   bool     // --no-cache
}

// BuildResult is the JSON shape for build.
type BuildResult struct {
	Tool       string      `json:"tool"`
	Context    string      `json:"context"`
	File       string      `json:"file"`
	Tags       []string    `json:"tags"`
	Platform   string      `json:"platform,omitempty"`
	ImageID    string      `json:"image_id"`
	DurationMS int64       `json:"duration_ms"`
	Push       *PushResult `json:"push,omitempty"`
}

// buildArgs is the exact argv after the binary, in a fixed order so tests
// and humans can read it: options first, tags, then the context.
func buildArgs(opt BuildOptions) []string {
	args := []string{"build"}
	if opt.File != "" {
		args = append(args, "-f", opt.File)
	}
	if opt.Platform != "" {
		args = append(args, "--platform", opt.Platform)
	}
	for _, kv := range opt.BuildArgs {
		args = append(args, "--build-arg", kv)
	}
	if opt.Target != "" {
		args = append(args, "--target", opt.Target)
	}
	if opt.NoCache {
		args = append(args, "--no-cache")
	}
	for _, tag := range opt.Tags {
		args = append(args, "-t", tag)
	}
	return append(args, opt.Context)
}

// Build runs the tool's build with its output streamed to log, then reads the
// image id back.
func (c *Client) Build(ctx context.Context, opt BuildOptions, log io.Writer) (*BuildResult, error) {
	if len(opt.Tags) == 0 {
		return nil, errors.New("build needs at least one -t <image:tag>; push and inspect have nothing to name otherwise")
	}
	if opt.Context == "" {
		opt.Context = "."
	}
	for _, tag := range opt.Tags {
		if _, err := Registry(tag); err != nil {
			return nil, err
		}
	}
	start := time.Now()
	if err := c.r.stream(ctx, log, log, buildArgs(opt)...); err != nil {
		return nil, err
	}
	id, err := c.imageID(ctx, opt.Tags[0])
	if err != nil {
		return nil, err
	}
	return &BuildResult{
		Tool:       c.Tool().String(),
		Context:    opt.Context,
		File:       opt.File,
		Tags:       opt.Tags,
		Platform:   opt.Platform,
		ImageID:    id,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// imageID normalises the two tools: Docker prints "sha256:<hex>", Podman
// prints the bare hex.
func (c *Client) imageID(ctx context.Context, ref string) (string, error) {
	out, err := c.r.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", fmt.Errorf("image id of %s: %w", ref, err)
	}
	id := strings.TrimSpace(string(out))
	if id != "" && !strings.Contains(id, ":") {
		id = "sha256:" + id
	}
	return id, nil
}
