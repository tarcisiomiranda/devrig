package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

type commandDaemon struct {
	mu          sync.Mutex
	exec        container.ExecOptions
	run         container.Config
	host        container.HostConfig
	deleted     []string
	filters     []string
	stdin       chan []byte
	started     chan struct{}
	exitCode    int
	startError  bool
	createError bool
	removeError bool
	block       bool
	missing     bool
	legacy      bool
	stdout      string
	stderr      string
}

func newCommandDaemon(t *testing.T) (*Client, *commandDaemon) {
	t.Helper()
	d := &commandDaemon{stdin: make(chan []byte, 1), started: make(chan struct{}), stdout: "out\n", stderr: "err\n"}
	server := httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(server.Close)
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithVersion("1.48"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return &Client{cli: cli}, d
}

func (d *commandDaemon) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1.48")
	switch {
	case path == "/containers/json":
		d.mu.Lock()
		d.filters = append(d.filters, r.URL.Query().Get("filters"))
		d.mu.Unlock()
		labels := map[string]string{
			instance.LabelManaged: "1", instance.LabelName: "app", instance.LabelDB: "actual_database",
			instance.LabelUser: "actual_user", instance.LabelPass: "actual_password", instance.LabelEngine: "postgres",
			instance.LabelContainerPort: "6432",
		}
		filter := r.URL.Query().Get("filters")
		found := strings.Contains(filter, instance.LabelManaged+"=1")
		if d.legacy {
			found = strings.Contains(filter, instance.LegacyLabelManaged+"=1")
			labels = map[string]string{
				instance.LegacyLabelManaged: "1", instance.LegacyLabelName: "app",
				instance.LegacyLabelDB: "legacy_real_database", instance.LegacyLabelUser: "legacy_user", instance.LegacyLabelPass: "legacy_password",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if d.missing || !found {
			io.WriteString(w, `[]`)
			return
		}
		json.NewEncoder(w).Encode([]container.Summary{{ID: "target-full-id", State: "running", Image: "postgres", Labels: labels}})
	case path == "/containers/target-full-id/json":
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Id":"target-full-id","Name":"/devrig-app","State":{"Status":"running","Running":true}}`)
	case strings.HasPrefix(path, "/images/"):
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Id":"image-id","Config":{"Cmd":["default-command"]}}`)
	case path == "/containers/target-full-id/exec":
		d.mu.Lock()
		json.NewDecoder(r.Body).Decode(&d.exec)
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"Id":"exec-id"}`)
	case path == "/exec/exec-id/start":
		d.stream(w, r, false)
	case path == "/exec/exec-id/json":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"ID":"exec-id","Running":false,"ExitCode":%d}`, d.exitCode)
	case path == "/containers/create":
		if d.createError {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			io.WriteString(w, `{"message":"name already in use"}`)
			return
		}
		var config struct {
			container.Config
			HostConfig container.HostConfig
		}
		json.NewDecoder(r.Body).Decode(&config)
		d.mu.Lock()
		d.run, d.host = config.Config, config.HostConfig
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"Id":"helper-full-id","Warnings":[]}`)
	case path == "/containers/helper-full-id/attach":
		d.stream(w, r, true)
	case path == "/containers/helper-full-id/start":
		close(d.started)
		if d.startError {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"message":"start failed"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case path == "/containers/helper-full-id/wait":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"StatusCode":%d}`, d.exitCode)
	case r.Method == http.MethodDelete:
		d.mu.Lock()
		d.deleted = append(d.deleted, path)
		d.mu.Unlock()
		if d.removeError {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"message":"cleanup failed"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"message":"unexpected request %s %s"}`, r.Method, path)
	}
}

func (d *commandDaemon) stream(w http.ResponseWriter, r *http.Request, run bool) {
	io.Copy(io.Discard, r.Body)
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	rw.Flush()
	if run {
		<-d.started
	}
	d.mu.Lock()
	input := d.exec.AttachStdin
	if run {
		input = d.run.OpenStdin
	}
	d.mu.Unlock()
	if d.block {
		io.Copy(io.Discard, rw)
		return
	}
	if input {
		data, _ := io.ReadAll(rw)
		d.stdin <- data
	}
	io.WriteString(stdcopy.NewStdWriter(conn, stdcopy.Stdout), d.stdout)
	io.WriteString(stdcopy.NewStdWriter(conn, stdcopy.Stderr), d.stderr)
}

func TestExecUsesExactArgumentsStreamsAndRealLabels(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			c, d := newCommandDaemon(t)
			d.legacy, d.exitCode = legacy, 7
			var stdout, stderr bytes.Buffer
			argv := []string{"psql", "-c", "SELECT $$'$HOME';\ná日本語$$;", "", "a\\b"}
			input := []byte{0, 0xff, '\n', '$', '\'', '"'}
			result, err := c.Exec(context.Background(), "app", rig.CommandOptions{
				Argv: argv, Stdin: bytes.NewReader(input), Stdout: &stdout, Stderr: &stderr,
				Env: map[string]string{"VALUE": "a b,=$'\"\n"}, Workdir: "/with space",
			})
			if err != nil {
				t.Fatal(err)
			}
			d.mu.Lock()
			config := d.exec
			filters := append([]string(nil), d.filters...)
			d.mu.Unlock()
			if !reflect.DeepEqual(config.Cmd, argv) || config.WorkingDir != "/with space" || config.Tty || !config.AttachStdin {
				t.Fatalf("exec config = %+v", config)
			}
			if result.ExitCode != 7 || stdout.String() != "out\n" || stderr.String() != "err\n" || !bytes.Equal(<-d.stdin, input) {
				t.Fatalf("result = %+v, stdout = %q, stderr = %q", result, stdout.String(), stderr.String())
			}
			database, port := "actual_database", "6432"
			if legacy {
				database, port = "legacy_real_database", "5432"
			}
			env := strings.Join(config.Env, "\n")
			if !strings.Contains(env, "PGDATABASE="+database) || !strings.Contains(env, "PGPORT="+port) || !strings.Contains(env, "PGHOST=127.0.0.1") || !strings.Contains(env, "VALUE=a b,=$'\"\n") {
				t.Fatalf("environment = %q", config.Env)
			}
			for _, filter := range filters {
				if !strings.Contains(filter, "=1") || !strings.Contains(filter, "name=app") {
					t.Fatalf("command lookup must require ownership and name labels: %s", filter)
				}
			}
		})
	}
}

func TestRunConnectsAttachesBeforeStartAndRemovesOnlyCreatedContainer(t *testing.T) {
	c, d := newCommandDaemon(t)
	d.exitCode = 13
	var stdout, stderr bytes.Buffer
	argv := []string{"pg_restore", "--dbname=a'b", "", "--list"}
	result, err := c.Run(context.Background(), rig.RunOptions{
		Name: "restore", Image: "backup:local", Connect: "app",
		CommandOptions: rig.CommandOptions{Argv: argv, Stdin: strings.NewReader("dump bytes"), Stdout: &stdout, Stderr: &stderr, Env: map[string]string{"PGDATABASE": "override"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !reflect.DeepEqual([]string(d.run.Entrypoint), argv[:1]) || !reflect.DeepEqual([]string(d.run.Cmd), argv[1:]) || d.host.NetworkMode != "container:target-full-id" || len(d.host.PortBindings) != 0 {
		t.Fatalf("run = %+v, host = %+v", d.run, d.host)
	}
	if d.run.Labels[instance.LabelManaged] != "1" || d.run.Labels[instance.LabelKind] != "run" || !reflect.DeepEqual(d.deleted, []string{"/containers/helper-full-id"}) {
		t.Fatalf("ownership = %+v, deletes = %v", d.run.Labels, d.deleted)
	}
	if result.ExitCode != 13 || stdout.String() != "out\n" || stderr.String() != "err\n" || string(<-d.stdin) != "dump bytes" {
		t.Fatalf("result = %+v, stdout = %q, stderr = %q", result, stdout.String(), stderr.String())
	}
	if !strings.Contains(strings.Join(d.run.Env, "\n"), "PGDATABASE=override") {
		t.Fatalf("explicit connection environment must win: %q", d.run.Env)
	}
}

func TestRunRemovesContainerOnStartFailureAndCancellation(t *testing.T) {
	for _, startError := range []bool{true, false} {
		t.Run(fmt.Sprint(startError), func(t *testing.T) {
			c, d := newCommandDaemon(t)
			d.startError, d.block = startError, true
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := c.Run(ctx, rig.RunOptions{Image: "helper", CommandOptions: rig.CommandOptions{Argv: []string{"sleep", "300"}}})
			if err == nil || (!startError && !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("err = %v", err)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if !reflect.DeepEqual(d.deleted, []string{"/containers/helper-full-id"}) {
				t.Fatalf("deletes = %v", d.deleted)
			}
		})
	}
}

func TestRunWithMissingConnectionDoesNotCreateOrRemoveAnything(t *testing.T) {
	c, d := newCommandDaemon(t)
	d.missing = true
	_, err := c.Run(context.Background(), rig.RunOptions{Image: "helper", Connect: "app"})
	if !rig.IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.run.Image != "" || len(d.deleted) > 0 {
		t.Fatalf("must not mutate when target is absent: run = %+v, deletes = %v", d.run, d.deleted)
	}
}

func TestRunDoesNotRemoveAConflictingName(t *testing.T) {
	c, d := newCommandDaemon(t)
	d.createError = true
	_, err := c.Run(context.Background(), rig.RunOptions{Image: "helper", Name: "app"})
	if err == nil || !strings.Contains(err.Error(), "name already in use") {
		t.Fatalf("err = %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.deleted) != 0 {
		t.Fatalf("deleted existing container: %v", d.deleted)
	}
}

func TestRunReportsCleanupFailure(t *testing.T) {
	c, d := newCommandDaemon(t)
	d.removeError = true
	result, err := c.Run(context.Background(), rig.RunOptions{Image: "helper"})
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") || result == nil {
		t.Fatalf("cleanup failure was lost: result = %+v, err = %v", result, err)
	}
}

type failingCommandInput struct{}

func (failingCommandInput) Read([]byte) (int, error) {
	return 0, errors.New("input read failed")
}

func TestRunReportsInputReadFailureAndCleansUp(t *testing.T) {
	c, d := newCommandDaemon(t)
	_, err := c.Run(context.Background(), rig.RunOptions{Image: "helper", CommandOptions: rig.CommandOptions{Stdin: failingCommandInput{}}})
	if err == nil || !strings.Contains(err.Error(), "input read failed") {
		t.Fatalf("input failure was lost: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !reflect.DeepEqual(d.deleted, []string{"/containers/helper-full-id"}) {
		t.Fatalf("deletes = %v", d.deleted)
	}
}
