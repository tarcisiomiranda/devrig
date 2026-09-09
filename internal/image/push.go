package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// PushOptions configures push.
type PushOptions struct {
	Refs    []string
	Login   bool     // false with --no-login: never touch credentials
	Sources []Source // where credentials are looked up, in order
	Keys    KeyOverrides
}

// Pushed is one pushed reference.
type Pushed struct {
	Image    string `json:"image"`
	Registry string `json:"registry"`
	Digest   string `json:"digest"`
}

// PushResult is the JSON shape for push.
type PushResult struct {
	Tool   string        `json:"tool"`
	Pushed []Pushed      `json:"pushed"`
	Logins []LoginResult `json:"logins"`
}

var dockerDigest = regexp.MustCompile(`digest:\s*(sha256:[0-9a-f]{64})`)

// parseDockerDigest finds the digest in `docker push` output.
func parseDockerDigest(out string) string {
	m := dockerDigest.FindStringSubmatch(out)
	if m == nil {
		return ""
	}
	return m[1]
}

// Push logs in once per registry (when credentials exist) and pushes each
// reference in order, stopping at the first failure.
func (c *Client) Push(ctx context.Context, opt PushOptions, log io.Writer) (*PushResult, error) {
	if len(opt.Refs) == 0 {
		return nil, errors.New("push needs at least one image reference")
	}
	registries := make([]string, len(opt.Refs))
	for i, ref := range opt.Refs {
		registry, err := Registry(ref)
		if err != nil {
			return nil, err
		}
		registries[i] = registry
	}
	res := &PushResult{Tool: c.Tool().String(), Pushed: []Pushed{}, Logins: []LoginResult{}}
	seen := map[string]bool{}
	for i, ref := range opt.Refs {
		registry := registries[i]
		if opt.Login && !seen[registry] {
			seen[registry] = true
			cred, err := Resolve(registry, opt.Keys, opt.Sources)
			switch {
			case IsNoCredentials(err):
				// The tool may hold a session already; let the push decide.
			case err != nil:
				return nil, err
			default:
				lr, err := c.Login(ctx, cred)
				if err != nil {
					return nil, err
				}
				res.Logins = append(res.Logins, *lr)
			}
		}
		digest, err := c.push(ctx, ref, registry, log)
		if err != nil {
			return nil, err
		}
		res.Pushed = append(res.Pushed, Pushed{Image: ref, Registry: registry, Digest: digest})
	}
	return res, nil
}

// push runs one `<tool> push`. Podman writes the digest to --digestfile;
// Docker prints it. Either way the digest is best effort.
func (c *Client) push(ctx context.Context, ref, registry string, log io.Writer) (string, error) {
	if c.Tool() == ToolPodman {
		tmp, err := os.CreateTemp("", "devrig-digest-*")
		if err != nil {
			return "", fmt.Errorf("digest file: %w", err)
		}
		path := tmp.Name()
		_ = tmp.Close()
		defer os.Remove(path)
		args := append([]string{"push", "--digestfile", path}, c.tlsArgs(registry)...)
		args = append(args, ref)
		if err := c.r.stream(ctx, log, log, args...); err != nil {
			return "", err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", nil
		}
		return strings.TrimSpace(string(raw)), nil
	}
	var out bytes.Buffer
	if err := c.r.stream(ctx, io.MultiWriter(log, &out), log, "push", ref); err != nil {
		return "", err
	}
	return parseDockerDigest(out.String()), nil
}
