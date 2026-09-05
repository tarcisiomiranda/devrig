package kube

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tarcisiomiranda/devrig/internal/engine"
	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

func testUpOptions() rig.UpOptions {
	spec := engine.Default()
	return rig.UpOptions{
		Name:          "valid-vfa",
		Engine:        spec,
		User:          "test",
		Password:      "s3cr3t",
		Database:      "valid_vfa_test",
		Image:         spec.DefaultImage,
		ContainerPort: spec.ContainerPort,
	}
}

func renderManifest(t *testing.T, p podSpec) map[string]any {
	t.Helper()
	b, err := manifest(p)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	return out
}

func TestManifestCarriesIdentityAndConfig(t *testing.T) {
	pod := renderManifest(t, podSpec{opt: testUpOptions(), namespace: "dev"})
	meta := pod["metadata"].(map[string]any)

	if got := meta["name"]; got != "devrig-valid-vfa" {
		t.Fatalf("pod name = %v, want devrig-valid-vfa", got)
	}
	if got := meta["namespace"]; got != "dev" {
		t.Fatalf("namespace = %v, want dev", got)
	}

	labels := meta["labels"].(map[string]any)
	if labels[instance.LabelManaged] != "1" || labels[instance.LabelName] != "valid-vfa" {
		t.Fatalf("labels do not identify the instance: %v", labels)
	}
	if labels[instance.LabelEngine] != "postgres" {
		t.Fatalf("engine label = %v, want postgres", labels[instance.LabelEngine])
	}

	// A password does not fit a label value, so configuration lives in annotations.
	ann := meta["annotations"].(map[string]any)
	if ann[instance.LabelPass] != "s3cr3t" || ann[instance.LabelDB] != "valid_vfa_test" {
		t.Fatalf("annotations lost the configuration: %v", ann)
	}
	if ann[instance.LabelContainerPort] != "5432" {
		t.Fatalf("container port annotation = %v, want 5432", ann[instance.LabelContainerPort])
	}
}

func TestManifestContainerMirrorsTheEngineSpec(t *testing.T) {
	pod := renderManifest(t, podSpec{opt: testUpOptions()})
	spec := pod["spec"].(map[string]any)
	container := spec["containers"].([]any)[0].(map[string]any)

	if container["image"] != engine.Default().DefaultImage {
		t.Fatalf("image = %v", container["image"])
	}
	env := map[string]string{}
	for _, e := range container["env"].([]any) {
		kv := e.(map[string]any)
		env[kv["name"].(string)] = kv["value"].(string)
	}
	if env["POSTGRES_PASSWORD"] != "s3cr3t" || env["POSTGRES_DB"] != "valid_vfa_test" {
		t.Fatalf("env does not come from the engine spec: %v", env)
	}
	port := container["ports"].([]any)[0].(map[string]any)
	if port["containerPort"].(float64) != 5432 {
		t.Fatalf("containerPort = %v, want 5432", port["containerPort"])
	}
}

// Without a readiness probe the pod counts as ready before the server listens,
// and kubectl port-forward dies with "lost connection to pod".
func TestManifestWaitsForTheServerToListen(t *testing.T) {
	pod := renderManifest(t, podSpec{opt: testUpOptions()})
	container := pod["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	probe, ok := container["readinessProbe"].(map[string]any)
	if !ok {
		t.Fatal("the pod has no readiness probe")
	}
	tcp := probe["tcpSocket"].(map[string]any)
	if tcp["port"].(float64) != 5432 {
		t.Fatalf("readiness probe port = %v, want the container port", tcp["port"])
	}
}

func TestManifestAlwaysSetsResources(t *testing.T) {
	pod := renderManifest(t, podSpec{opt: testUpOptions()})
	res := pod["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["resources"].(map[string]any)
	if res["requests"].(map[string]any)["cpu"] != DefaultCPURequest {
		t.Fatalf("default cpu request = %v", res["requests"])
	}

	custom := renderManifest(t, podSpec{opt: testUpOptions(), cpu: "250m", memory: "512Mi"})
	res = custom["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["resources"].(map[string]any)
	req := res["requests"].(map[string]any)
	lim := res["limits"].(map[string]any)
	if req["cpu"] != "250m" || lim["cpu"] != "250m" || req["memory"] != "512Mi" || lim["memory"] != "512Mi" {
		t.Fatalf("--cpu/--memory must set both sides: %v %v", req, lim)
	}
}

func TestManifestPodNameIsDNS1123(t *testing.T) {
	opt := testUpOptions()
	opt.Name = "Cyber_Valid_VFA"
	pod := renderManifest(t, podSpec{opt: opt})
	name := pod["metadata"].(map[string]any)["name"].(string)
	if strings.ContainsAny(name, "_ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Fatalf("pod name %q is not a DNS-1123 label", name)
	}
	if name != "devrig-cyber-valid-vfa" {
		t.Fatalf("pod name = %q", name)
	}
}
