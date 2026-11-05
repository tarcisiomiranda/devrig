//go:build windows

package docker

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func TestWindowsDockerNamedPipe(t *testing.T) {
	name := fmt.Sprintf("devrig-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	listener, err := winio.ListenPipe(`\\.\pipe\`+name, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_ping":
			w.Header().Set("API-Version", "1.48")
			fmt.Fprint(w, "OK")
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"OSType":"linux"}`)
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	t.Setenv("DOCKER_HOST", "npipe:////./pipe/"+name)
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_API_VERSION", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connected, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connected.Close()
}
