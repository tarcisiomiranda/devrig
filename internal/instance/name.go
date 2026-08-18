package instance

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/tarcisiomiranda/devrig/internal/engine"
)

const (
	LabelManaged = "devrig.managed"
	LabelName    = "devrig.name"
	LabelDB      = "devrig.db"
	LabelUser    = "devrig.user"
	LabelImage   = "devrig.image"
	LabelPass    = "devrig.password"
	LabelEngine  = "devrig.engine"

	// Containers created by the older cyber-specs "testpg" build. Kept so
	// list/down can still find and clean them up after the rename; devrig
	// never creates containers with these. Reading db/user/password matters:
	// guessing them from today's defaults would hand out a wrong URL.
	LegacyLabelManaged = "cyber.testpg"
	LegacyLabelName    = "cyber.testpg.name"
	LegacyLabelDB      = "cyber.testpg.db"
	LegacyLabelUser    = "cyber.testpg.user"
	LegacyLabelImage   = "cyber.testpg.image"
	LegacyLabelPass    = "cyber.testpg.password"

	DefaultHost = "127.0.0.1"

	// Container name prefix, also the legacy one for lookups.
	namePrefix       = "devrig-"
	legacyNamePrefix = "testpg-"
)

// Throwaway credentials shared by every engine: these containers bind to
// loopback and exist only for the length of a test run.
const (
	DefaultUser     = "test"
	DefaultPassword = "test"
)

// DefaultImage is the default engine's image; every engine carries its own
// in its engine.Spec.
var DefaultImage = engine.Default().DefaultImage

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// ValidateName ensures the instance name is safe for Docker labels and container names.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: use letters, digits, _ or - (max 63 chars, must start alnum)", name)
	}
	return nil
}

// SanitizeForDB turns an instance name into a safe SQL identifier fragment.
func SanitizeForDB(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "app"
	}
	// Postgres caps identifiers at 63 bytes; MySQL at 64. Leave room for _test.
	const max = 48
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// DefaultDatabase returns <name>_test. Projects with a naming convention
// (e.g. Cyber's cyber_<system>_test) should pass --db explicitly.
func DefaultDatabase(name string) string {
	return SanitizeForDB(name) + "_test"
}

// ContainerName is the Docker container name for an instance.
func ContainerName(name string) string {
	return namePrefix + SanitizeForDB(name)
}

// LegacyContainerName is the name used by the old testpg build.
func LegacyContainerName(name string) string {
	return legacyNamePrefix + SanitizeForDB(name)
}

// URL builds a connection string for the given engine.
func URL(spec *engine.Spec, user, password, host string, port int, database string) string {
	return spec.URL(user, password, host, port, database)
}
