// Package engine describes the databases devrig knows how to run.
//
// Everything database-specific lives here: the image, the port inside the
// container, the environment the server needs, and how a connection URL is
// built. The Docker layer stays engine-agnostic and just asks for a Spec.
//
// Only Postgres is implemented today. MySQL and MariaDB are declared but
// disabled — see TODO.md for the exact work each one needs.
package engine

import (
	"fmt"
	"sort"
	"strings"
)

// Name identifies a supported database engine.
type Name string

func (n Name) String() string { return string(n) }

const (
	Postgres Name = "postgres"
	MySQL    Name = "mysql"
	MariaDB  Name = "mariadb"
)

// Spec is everything devrig needs to run one database engine.
type Spec struct {
	Name Name

	// DefaultImage is the container image used when --image is not given.
	DefaultImage string

	// ContainerPort is the port the server listens on inside the container.
	ContainerPort int

	// DefaultUser and DefaultPassword are throwaway credentials: these
	// containers are for tests and are never exposed beyond loopback.
	DefaultUser     string
	DefaultPassword string

	// Env builds the container environment that creates the initial
	// user/password/database on first boot.
	Env func(user, password, database string) []string

	// URL builds the connection string handed to callers.
	URL func(user, password, host string, port int, database string) string

	// Implemented reports whether devrig can actually start this engine.
	// Declared-but-unimplemented engines still show up in errors and help,
	// so users learn what is coming instead of getting "unknown engine".
	Implemented bool
}

var specs = map[Name]*Spec{
	Postgres: {
		Name:            Postgres,
		DefaultImage:    "postgres:16-alpine",
		ContainerPort:   5432,
		DefaultUser:     "test",
		DefaultPassword: "test",
		Env: func(user, password, database string) []string {
			return []string{
				"POSTGRES_USER=" + user,
				"POSTGRES_PASSWORD=" + password,
				"POSTGRES_DB=" + database,
			}
		},
		// sslmode=disable: a throwaway local Postgres has no TLS, and many
		// drivers refuse to connect without saying so explicitly.
		URL: func(user, password, host string, port int, database string) string {
			return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
				user, password, host, port, database)
		},
		Implemented: true,
	},

	// TODO(mysql): implement. Needs MYSQL_ROOT_PASSWORD (or
	// MYSQL_ALLOW_EMPTY_PASSWORD) plus MYSQL_USER/PASSWORD/DATABASE, a
	// readiness probe that is not pgx, and a mysql:// DSN. See TODO.md.
	MySQL: {
		Name:            MySQL,
		DefaultImage:    "mysql:8",
		ContainerPort:   3306,
		DefaultUser:     "test",
		DefaultPassword: "test",
		Implemented:     false,
	},

	// TODO(mariadb): implement. Same shape as MySQL (MARIADB_* env vars are
	// accepted by recent images, MYSQL_* still work). See TODO.md.
	MariaDB: {
		Name:            MariaDB,
		DefaultImage:    "mariadb:11",
		ContainerPort:   3306,
		DefaultUser:     "test",
		DefaultPassword: "test",
		Implemented:     false,
	},
}

// ErrUnknown is returned for a resource devrig has never heard of. It is a
// distinct type so callers can tell "typo / old CLI syntax" from "planned".
type ErrUnknown struct{ Name string }

func (e *ErrUnknown) Error() string {
	return fmt.Sprintf("unknown resource %q — known: %s", e.Name, strings.Join(Known(), ", "))
}

// ErrUnsupported is returned for engines that are declared but not built yet.
type ErrUnsupported struct{ Name Name }

func (e *ErrUnsupported) Error() string {
	return fmt.Sprintf(
		"engine %q is not implemented yet (planned; see TODO.md) — available now: %s",
		e.Name, strings.Join(Implemented(), ", "),
	)
}

// Default is the engine used when the caller does not pick one.
func Default() *Spec { return specs[Postgres] }

// Lookup returns the spec for an engine name.
func Lookup(name string) (*Spec, error) {
	if name == "" {
		return specs[Postgres], nil
	}
	spec, ok := specs[Name(strings.ToLower(strings.TrimSpace(name)))]
	if !ok {
		return nil, &ErrUnknown{Name: name}
	}
	if !spec.Implemented {
		return nil, &ErrUnsupported{Name: spec.Name}
	}
	return spec, nil
}

// Known lists every engine name devrig is aware of, implemented or not.
func Known() []string {
	out := make([]string, 0, len(specs))
	for n := range specs {
		out = append(out, string(n))
	}
	sort.Strings(out)
	return out
}

// Implemented lists the engines that actually work today.
func Implemented() []string {
	out := make([]string, 0, len(specs))
	for n, s := range specs {
		if s.Implemented {
			out = append(out, string(n))
		}
	}
	sort.Strings(out)
	return out
}
