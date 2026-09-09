// Package envfile reads registry credentials and other settings from a
// KEY=VALUE file (or a JSON, YAML or TOML file) through confctl, so devrig
// never carries its own parser.
package envfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultBinary is the confctl executable looked up in PATH.
const DefaultBinary = "confctl"

// InstallHint is the one-line installer for confctl.
const InstallHint = "curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/confctl/main/install.sh | bash"

// Runner executes confctl. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct{ bin string }

func (r *execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// confctl's stdout is the parsed secret map by construction; it must
		// never reach an error string, so only stderr is ever appended here.
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, fmt.Errorf("confctl %s: %w\n%s", strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("confctl %s: %w", strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}

// MissingBinaryError says confctl is not installed.
type MissingBinaryError struct {
	Binary string
	Err    error
}

func (e *MissingBinaryError) Error() string {
	return fmt.Sprintf("%s not found in PATH (needed to read the env file): %v\n"+
		"hint: %s\n"+
		"      or set DEVRIG_CONFCTL, or pass --no-login to push without credentials",
		e.Binary, e.Err, InstallHint)
}

func (e *MissingBinaryError) Unwrap() error { return e.Err }

// NewRunner resolves the confctl binary: the given name, else DefaultBinary.
func NewRunner(binary string) (Runner, error) {
	if binary == "" {
		binary = DefaultBinary
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, &MissingBinaryError{Binary: binary, Err: err}
	}
	return &execRunner{bin: path}, nil
}

// FormatArgs returns the --format flag confctl needs for path: nothing for
// the structured formats it detects by extension, "--format env" otherwise,
// so ".env", ".env.prod" and "secrets.env" all parse as KEY=VALUE.
func FormatArgs(path string) []string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".yaml", ".yml", ".toml":
		return nil
	}
	return []string{"--format", "env"}
}

// Read parses the file into string values. Numbers and booleans are
// stringified (a token that happens to be all digits is still a token);
// nested objects and arrays are skipped.
func Read(ctx context.Context, r Runner, path string) (map[string]string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("env file: %w", err)
	}
	args := append([]string{"-c"}, FormatArgs(path)...)
	args = append(args, path)
	raw, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	var parsed map[string]any
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("confctl output for %s is not a JSON object: %w", path, err)
	}
	values := make(map[string]string, len(parsed))
	for k, v := range parsed {
		if s, ok := scalar(v); ok {
			values[k] = s
		}
	}
	return values, nil
}

func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case json.Number:
		return t.String(), true
	}
	return "", false
}
