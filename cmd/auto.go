package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/tarcisiomiranda/devrig/internal/kube"
	"github.com/tarcisiomiranda/devrig/internal/rig"
)

// autoBackend runs new instances on the primary backend but looks for existing
// ones on both, so `up --backend kube` followed by a bare `url` still resolves.
type autoBackend struct {
	primary  rig.Backend
	fallback func(ctx context.Context) (rig.Backend, error)

	once   sync.Once
	second rig.Backend
	err    error
}

func newAutoBackend(primary rig.Backend, opt kube.Options) *autoBackend {
	return &autoBackend{
		primary: primary,
		fallback: func(ctx context.Context) (rig.Backend, error) {
			return kube.New(ctx, opt)
		},
	}
}

func (a *autoBackend) secondary(ctx context.Context) rig.Backend {
	a.once.Do(func() { a.second, a.err = a.fallback(ctx) })
	if a.err != nil {
		return nil
	}
	return a.second
}

func (a *autoBackend) Kind() rig.Kind { return a.primary.Kind() }

func (a *autoBackend) Close() error {
	if a.second != nil {
		_ = a.second.Close()
	}
	return a.primary.Close()
}

func (a *autoBackend) Up(ctx context.Context, opt rig.UpOptions) (*rig.Status, error) {
	return a.primary.Up(ctx, opt)
}

func (a *autoBackend) Status(ctx context.Context, name string) (*rig.Status, error) {
	st, err := a.primary.Status(ctx, name)
	if !rig.IsNotFound(err) {
		return st, err
	}
	if other := a.secondary(ctx); other != nil {
		return other.Status(ctx, name)
	}
	return st, err
}

func (a *autoBackend) URL(ctx context.Context, name string) (string, error) {
	u, err := a.primary.URL(ctx, name)
	if !rig.IsNotFound(err) {
		return u, err
	}
	if other := a.secondary(ctx); other != nil {
		return other.URL(ctx, name)
	}
	return u, err
}

func (a *autoBackend) Logs(ctx context.Context, name string, tail int) (*rig.LogsResult, error) {
	res, err := a.primary.Logs(ctx, name, tail)
	if !rig.IsNotFound(err) {
		return res, err
	}
	if other := a.secondary(ctx); other != nil {
		return other.Logs(ctx, name, tail)
	}
	return res, err
}

// Down is idempotent on both backends, so a name that only exists on the
// second one is still removed.
func (a *autoBackend) Down(ctx context.Context, name string) (*rig.DownResult, error) {
	res, err := a.primary.Down(ctx, name)
	if err != nil && !rig.IsNotFound(err) {
		return nil, err
	}
	if res != nil && res.Removed {
		return res, nil
	}
	other := a.secondary(ctx)
	if other == nil {
		if res != nil {
			return res, nil
		}
		return nil, err
	}
	second, secondErr := other.Down(ctx, name)
	if secondErr != nil {
		if res != nil {
			return res, nil
		}
		return nil, secondErr
	}
	if second.Removed || res == nil {
		return second, nil
	}
	return res, nil
}

func (a *autoBackend) List(ctx context.Context) ([]rig.Status, error) {
	items, err := a.primary.List(ctx)
	if err != nil {
		return nil, err
	}
	other := a.secondary(ctx)
	if other == nil {
		return items, nil
	}
	more, err := other.List(ctx)
	if err != nil {
		return items, nil
	}
	return append(items, more...), nil
}

func backendUnavailable(dockerErr, kubeErr error) error {
	return fmt.Errorf("no backend available\n  docker: %v\n  kubernetes: %v\n"+
		"hint: start Docker, or point kubectl at a cluster and rerun with --backend kube",
		firstLine(dockerErr), firstLine(kubeErr))
}
