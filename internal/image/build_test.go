package image

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/o/a:t":                      "ghcr.io",
		"o/a":                                "docker.io",
		"app":                                "docker.io",
		"localhost:5000/a":                   "localhost:5000",
		"us-central1-docker.pkg.dev/p/r/i:d": "us-central1-docker.pkg.dev",
	} {
		got, err := Registry(ref)
		if err != nil {
			t.Fatalf("Registry(%q): %v", ref, err)
		}
		if got != want {
			t.Errorf("Registry(%q) = %q, want %q", ref, got, want)
		}
	}
	if _, err := Registry("Bad Ref"); err == nil {
		t.Fatal("expected an error for an invalid reference")
	}
}

func TestBuildArgsOrderAndFlags(t *testing.T) {
	got := strings.Join(buildArgs(BuildOptions{
		Context:   "./svc",
		File:      "docker/Dockerfile",
		Tags:      []string{"ghcr.io/o/a:dev", "ghcr.io/o/a:latest"},
		Platform:  "linux/amd64",
		BuildArgs: []string{"VERSION=1.2", "BUILD_ENV=dev"},
		Target:    "runtime",
		NoCache:   true,
	}), " ")
	want := "build -f docker/Dockerfile --platform linux/amd64 --build-arg VERSION=1.2 --build-arg BUILD_ENV=dev" +
		" --target runtime --no-cache -t ghcr.io/o/a:dev -t ghcr.io/o/a:latest ./svc"
	if got != want {
		t.Fatalf("argv =\n%s\nwant\n%s", got, want)
	}
}

func TestBuildArgsMinimal(t *testing.T) {
	got := strings.Join(buildArgs(BuildOptions{Context: ".", Tags: []string{"a:1"}}), " ")
	if got != "build -t a:1 ." {
		t.Fatalf("argv = %q", got)
	}
}

func TestBuildDefaultsContextAndReadsImageID(t *testing.T) {
	c, f := testClient(ToolPodman)
	f.out["image inspect"] = []byte("abc123\n")
	var log bytes.Buffer
	res, err := c.Build(context.Background(), BuildOptions{Tags: []string{"ghcr.io/o/a:dev"}}, &log)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	argv := f.argv()
	if len(argv) != 2 || argv[0] != "build -t ghcr.io/o/a:dev ." || argv[1] != "image inspect --format {{.Id}} ghcr.io/o/a:dev" {
		t.Fatalf("argv = %v", argv)
	}
	if res.ImageID != "sha256:abc123" || res.Context != "." || res.Tool != "podman" || res.File != "" {
		t.Fatalf("result = %+v", res)
	}
	if res.Push != nil {
		t.Fatal("push must be nil without --push")
	}
}

func TestBuildKeepsDockerImageIDPrefix(t *testing.T) {
	c, f := testClient(ToolDocker)
	f.out["image inspect"] = []byte("sha256:def456\n")
	res, err := c.Build(context.Background(), BuildOptions{Tags: []string{"a:1"}}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.ImageID != "sha256:def456" {
		t.Fatalf("image id = %q", res.ImageID)
	}
}

func TestBuildRequiresATag(t *testing.T) {
	c, f := testClient(ToolPodman)
	_, err := c.Build(context.Background(), BuildOptions{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "-t") {
		t.Fatalf("err = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing must run without a tag")
	}
}

func TestBuildRejectsInvalidTagBeforeRunning(t *testing.T) {
	c, f := testClient(ToolPodman)
	if _, err := c.Build(context.Background(), BuildOptions{Tags: []string{"Bad Tag"}}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error")
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing must run with an invalid tag")
	}
}

func TestBuildStreamsToolOutputToTheLog(t *testing.T) {
	c, f := testClient(ToolPodman)
	f.out["build"] = []byte("STEP 1/2: FROM alpine\n")
	f.out["image inspect"] = []byte("abc\n")
	var log bytes.Buffer
	if _, err := c.Build(context.Background(), BuildOptions{Tags: []string{"a:1"}}, &log); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(log.String(), "STEP 1/2") {
		t.Fatalf("log = %q", log.String())
	}
}

func TestBuildFailureStopsBeforeInspect(t *testing.T) {
	c, f := testClient(ToolPodman)
	f.err["build"] = errors.New("exit status 1")
	_, err := c.Build(context.Background(), BuildOptions{Tags: []string{"a:1"}}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(f.calls) != 1 {
		t.Fatalf("calls = %v, want only the build", f.argv())
	}
}
