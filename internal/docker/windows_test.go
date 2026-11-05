package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

func TestWindowsEngineRequiresLinux(t *testing.T) {
	for _, engineOS := range []string{"linux", "windows", ""} {
		t.Run(engineOS, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_ping" {
					w.Header().Set("API-Version", "1.48")
					fmt.Fprint(w, "OK")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/info") {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"OSType":%q}`, engineOS)
					return
				}
				http.NotFound(w, r)
			}))
			defer server.Close()
			connected, err := newClient(context.Background(), "windows", client.WithHost(server.URL), client.WithAPIVersionNegotiation())
			if engineOS == "linux" {
				if err != nil {
					t.Fatal(err)
				}
				defer connected.Close()
			} else if err == nil || !strings.Contains(err.Error(), "Linux containers") {
				t.Fatalf("expected Linux engine error, got %v", err)
			}
		})
	}
}
