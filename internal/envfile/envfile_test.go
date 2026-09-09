package envfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	out  []byte
	err  error
	args []string
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.args = args
	return f.out, f.err
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFormatArgs(t *testing.T) {
	for path, want := range map[string]string{
		".env":        "--format env",
		".env.prod":   "--format env",
		"secrets.env": "--format env",
		"deploy/.env": "--format env",
		"config.yaml": "",
		"config.yml":  "",
		"config.json": "",
		"config.toml": "",
		"Config.YAML": "",
	} {
		got := strings.Join(FormatArgs(path), " ")
		if got != want {
			t.Errorf("FormatArgs(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestReadPassesCompactFlagFormatAndPath(t *testing.T) {
	path := writeTemp(t, ".env", "A=1\n")
	f := &fakeRunner{out: []byte(`{"A":"1"}`)}
	if _, err := Read(context.Background(), f, path); err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := "-c --format env " + path
	if got := strings.Join(f.args, " "); got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// confctl types .env values: PORT=5432 is a number, FLAG=true a boolean. A
// token made of digits must survive as the exact string.
func TestReadStringifiesScalarsAndSkipsNested(t *testing.T) {
	path := writeTemp(t, "c.yaml", "x: 1\n")
	f := &fakeRunner{out: []byte(`{"USER":"bob","PIN":123456789012345678901,"ON":true,"nested":{"a":1},"list":[1]}`)}
	got, err := Read(context.Background(), f, path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["USER"] != "bob" || got["PIN"] != "123456789012345678901" || got["ON"] != "true" {
		t.Fatalf("values = %v", got)
	}
	if _, ok := got["nested"]; ok {
		t.Fatal("nested objects must be skipped")
	}
	if _, ok := got["list"]; ok {
		t.Fatal("arrays must be skipped")
	}
	if got := strings.Join(f.args, " "); got != "-c "+path {
		t.Fatalf("yaml must not get --format env, args = %q", got)
	}
}

// confctl reports a missing file as "Key not found", so devrig checks first.
func TestReadFailsBeforeConfctlWhenFileIsMissing(t *testing.T) {
	f := &fakeRunner{}
	_, err := Read(context.Background(), f, filepath.Join(t.TempDir(), "nope.env"))
	if err == nil || !strings.Contains(err.Error(), "env file") {
		t.Fatalf("err = %v, want an env file error", err)
	}
	if f.args != nil {
		t.Fatal("confctl must not run for a missing file")
	}
}

func TestReadRejectsNonObjectOutput(t *testing.T) {
	path := writeTemp(t, ".env", "A=1\n")
	f := &fakeRunner{out: []byte(`["not","an","object"]`)}
	if _, err := Read(context.Background(), f, path); err == nil {
		t.Fatal("expected an error for a non-object")
	}
}

func TestReadPropagatesRunnerErrors(t *testing.T) {
	path := writeTemp(t, ".env", "A=1\n")
	f := &fakeRunner{err: errors.New("confctl exploded")}
	if _, err := Read(context.Background(), f, path); err == nil || !strings.Contains(err.Error(), "confctl exploded") {
		t.Fatalf("err = %v", err)
	}
}

// confctl's stdout is the parsed secret map by construction; it must never
// reach an error string even when confctl exits non-zero with empty stderr.
func TestExecRunnerNeverEchoesStdoutIntoErrors(t *testing.T) {
	r, err := NewRunner("testdata/confctl-fails-with-stdout.sh")
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	_, err = r.Run(context.Background(), "-c", "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "leaked-token-value") {
		t.Fatalf("error must never echo confctl's stdout: %v", err)
	}
}

// The stderr path must stay covered: a real failure message still reaches
// the user.
func TestExecRunnerKeepsStderrInErrors(t *testing.T) {
	r, err := NewRunner("testdata/confctl-fails-with-stderr.sh")
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	_, err = r.Run(context.Background(), "-c", "x")
	if err == nil || !strings.Contains(err.Error(), "bad thing") {
		t.Fatalf("err = %v, want it to contain stderr's \"bad thing\"", err)
	}
}

func TestNewRunnerMissingBinaryCarriesInstallHint(t *testing.T) {
	_, err := NewRunner("confctl-definitely-not-installed-xyz")
	var missing *MissingBinaryError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want MissingBinaryError", err)
	}
	if !strings.Contains(err.Error(), InstallHint) || !strings.Contains(err.Error(), "--no-login") {
		t.Fatalf("error must carry the installer and the --no-login escape: %v", err)
	}
}
