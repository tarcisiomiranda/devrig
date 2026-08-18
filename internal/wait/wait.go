// Package wait blocks until a database container answers queries.
package wait

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tarcisiomiranda/devrig/internal/engine"
)

// Ready waits until host:port accepts TCP and the engine answers a trivial query.
func Ready(ctx context.Context, spec *engine.Spec, url string, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	name := spec.Name.String()
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s not ready at %s: %w", name, addr, err)
		}

		if err := probeTCP(ctx, addr); err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s not ready at %s: %w", name, addr, ctx.Err())
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}

		if err := probeQuery(ctx, spec, url); err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s not query-ready at %s: %w", name, addr, ctx.Err())
			case <-time.After(250 * time.Millisecond):
				continue
			}
		}
		return nil
	}
}

func probeTCP(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	_ = c.Close()
	return nil
}

func probeQuery(ctx context.Context, spec *engine.Spec, url string) error {
	switch spec.Name {
	case engine.Postgres:
		return probePostgres(ctx, url)
	default:
		return fmt.Errorf("no readiness probe implemented for engine %q", spec.Name)
	}
}

func probePostgres(ctx context.Context, url string) error {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := pgx.Connect(cctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	var n int
	if err := conn.QueryRow(cctx, "SELECT 1").Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("unexpected SELECT 1 result: %d", n)
	}
	return nil
}
