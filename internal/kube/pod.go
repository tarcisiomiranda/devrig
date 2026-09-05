package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/tarcisiomiranda/devrig/internal/instance"
)

type pod struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		DeletionTimestamp string            `json:"deletionTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
			Ports []struct {
				ContainerPort int `json:"containerPort"`
			} `json:"ports"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		Reason            string `json:"reason"`
		Message           string `json:"message"`
		ContainerStatuses []struct {
			Ready        bool `json:"ready"`
			RestartCount int  `json:"restartCount"`
			State        struct {
				Waiting *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"waiting"`
				Terminated *struct {
					Reason   string `json:"reason"`
					ExitCode int    `json:"exitCode"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type podList struct {
	Items []pod `json:"items"`
}

func (p *pod) instanceName() string {
	if v := p.Metadata.Labels[instance.LabelName]; v != "" {
		return v
	}
	return strings.TrimPrefix(p.Metadata.Name, "devrig-")
}

func (p *pod) annotation(key string) string { return p.Metadata.Annotations[key] }

func (p *pod) containerPort(fallback int) int {
	if v := p.annotation(instance.LabelContainerPort); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	for _, c := range p.Spec.Containers {
		for _, port := range c.Ports {
			if port.ContainerPort > 0 {
				return port.ContainerPort
			}
		}
	}
	return fallback
}

func (p *pod) image() string {
	if v := p.annotation(instance.LabelImage); v != "" {
		return v
	}
	if len(p.Spec.Containers) > 0 {
		return p.Spec.Containers[0].Image
	}
	return ""
}

func (p *pod) containerName(fallback string) string {
	if len(p.Spec.Containers) > 0 && p.Spec.Containers[0].Name != "" {
		return p.Spec.Containers[0].Name
	}
	return fallback
}

// state maps the pod phase onto the same vocabulary the Docker backend prints.
func (p *pod) state() string {
	if p.Metadata.DeletionTimestamp != "" {
		return "terminating"
	}
	switch p.Status.Phase {
	case "Running":
		return "running"
	case "Pending":
		return "pending"
	case "Succeeded":
		return "exited"
	case "Failed":
		return "failed"
	default:
		return strings.ToLower(p.Status.Phase)
	}
}

// blocked reports the container-level reason a pod is not running yet.
func (p *pod) blocked() string {
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && w.Reason != "" {
			if w.Message != "" {
				return w.Reason + ": " + strings.TrimSpace(w.Message)
			}
			return w.Reason
		}
		if t := cs.State.Terminated; t != nil && t.Reason != "" {
			return fmt.Sprintf("%s (exit %d)", t.Reason, t.ExitCode)
		}
	}
	if p.Status.Reason != "" {
		return p.Status.Reason
	}
	return ""
}

func (p *pod) running() bool {
	if p.Metadata.DeletionTimestamp != "" || p.Status.Phase != "Running" {
		return false
	}
	for _, cs := range p.Status.ContainerStatuses {
		if !cs.Ready {
			return false
		}
	}
	return true
}

func (c *Client) getPods(ctx context.Context, args ...string) ([]pod, error) {
	out, err := c.kubectl.run(ctx, nil, append([]string{"get", "pods", "-o", "json"}, args...)...)
	if err != nil {
		return nil, err
	}
	var list podList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse kubectl get pods: %w", err)
	}
	return list.Items, nil
}
