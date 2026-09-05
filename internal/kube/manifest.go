package kube

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tarcisiomiranda/devrig/internal/instance"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

// Resource requests and limits every devrig pod carries, so namespaces with a
// ResourceQuota or a LimitRange accept it.
const (
	DefaultCPURequest    = "100m"
	DefaultMemoryRequest = "256Mi"
	DefaultCPULimit      = "1"
	DefaultMemoryLimit   = "1Gi"
)

type podSpec struct {
	opt       rig.UpOptions
	namespace string
	cpu       string
	memory    string
}

func envPairs(env []string) []map[string]string {
	out := make([]map[string]string, 0, len(env))
	for _, kv := range env {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		out = append(out, map[string]string{"name": k, "value": v})
	}
	return out
}

func resources(cpu, memory string) map[string]any {
	reqCPU, reqMem := DefaultCPURequest, DefaultMemoryRequest
	limCPU, limMem := DefaultCPULimit, DefaultMemoryLimit
	if cpu != "" {
		reqCPU, limCPU = cpu, cpu
	}
	if memory != "" {
		reqMem, limMem = memory, memory
	}
	return map[string]any{
		"requests": map[string]string{"cpu": reqCPU, "memory": reqMem},
		"limits":   map[string]string{"cpu": limCPU, "memory": limMem},
	}
}

// manifest renders the pod devrig creates for one instance.
func manifest(p podSpec) ([]byte, error) {
	opt := p.opt
	meta := map[string]any{
		"name": instance.PodName(opt.Name),
		"labels": map[string]string{
			instance.LabelManaged: "1",
			instance.LabelName:    opt.Name,
			instance.LabelEngine:  opt.Engine.Name.String(),
		},
		"annotations": map[string]string{
			instance.LabelDB:            opt.Database,
			instance.LabelUser:          opt.User,
			instance.LabelPass:          opt.Password,
			instance.LabelImage:         opt.Image,
			instance.LabelContainerPort: strconv.Itoa(opt.ContainerPort),
		},
	}
	if p.namespace != "" {
		meta["namespace"] = p.namespace
	}

	pod := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   meta,
		"spec": map[string]any{
			"restartPolicy":                 "Always",
			"terminationGracePeriodSeconds": 5,
			"automountServiceAccountToken":  false,
			"containers": []map[string]any{{
				"name":  opt.Engine.Name.String(),
				"image": opt.Image,
				"env":   envPairs(opt.Engine.Env(opt.User, opt.Password, opt.Database)),
				"ports": []map[string]any{{
					"name":          "db",
					"containerPort": opt.ContainerPort,
					"protocol":      "TCP",
				}},
				"resources": resources(p.cpu, p.memory),
				"readinessProbe": map[string]any{
					"tcpSocket":           map[string]any{"port": opt.ContainerPort},
					"initialDelaySeconds": 1,
					"periodSeconds":       2,
					"timeoutSeconds":      2,
					"failureThreshold":    60,
				},
			}},
		},
	}
	return json.Marshal(pod)
}
