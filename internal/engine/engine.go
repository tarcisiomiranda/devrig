// Package engine holds every database-specific detail devrig needs.
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

	DefaultImage  string
	ContainerPort int

	DefaultUser     string
	DefaultPassword string

	Env func(user, password, database string) []string
	URL func(user, password, host string, port int, database string) string

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
		// sslmode=disable: local throwaway Postgres has no TLS.
		URL: func(user, password, host string, port int, database string) string {
			return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
				user, password, host, port, database)
		},
		Implemented: true,
	},

	MySQL: {
		Name:            MySQL,
		DefaultImage:    "mysql:8",
		ContainerPort:   3306,
		DefaultUser:     "test",
		DefaultPassword: "test",
		Implemented:     false,
	},

	MariaDB: {
		Name:            MariaDB,
		DefaultImage:    "mariadb:11",
		ContainerPort:   3306,
		DefaultUser:     "test",
		DefaultPassword: "test",
		Implemented:     false,
	},
}

// ErrUnknown is returned for a resource devrig has never heard of.
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
