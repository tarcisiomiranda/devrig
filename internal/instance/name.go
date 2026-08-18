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

	LegacyLabelManaged = "cyber.testpg"
	LegacyLabelName    = "cyber.testpg.name"
	LegacyLabelDB      = "cyber.testpg.db"
	LegacyLabelUser    = "cyber.testpg.user"
	LegacyLabelImage   = "cyber.testpg.image"
	LegacyLabelPass    = "cyber.testpg.password"

	DefaultHost = "127.0.0.1"

	namePrefix       = "devrig-"
	legacyNamePrefix = "testpg-"
)

// Throwaway credentials shared by every engine.
const (
	DefaultUser     = "test"
	DefaultPassword = "test"
)

// DefaultImage is the default engine's image.
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
	const maxIdentifierFragment = 48
	if len(out) > maxIdentifierFragment {
		out = out[:maxIdentifierFragment]
	}
	return out
}

// DefaultDatabase returns <name>_test.
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
