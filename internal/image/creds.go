package image

import (
	"errors"
	"fmt"
	"strings"
)

// Key names searched for each field, in order. A project with other names
// passes --user-key / --password-key / --registry-key.
var (
	RegistryKeys = []string{"REGISTRY", "CONTAINER_REGISTRY", "DOCKER_REGISTRY"}
	UserKeys     = []string{"REGISTRY_USER", "REGISTRY_USERNAME", "DOCKER_USER", "DOCKER_USERNAME"}
	PasswordKeys = []string{"REGISTRY_TOKEN", "REGISTRY_PASSWORD", "DOCKER_TOKEN", "DOCKER_PASSWORD"}
)

// Source is one place credentials can come from: a parsed env file or the
// process environment. Sources are searched in order; the first wins.
type Source struct {
	Name   string // ".env", the path given, or "environment"
	Values map[string]string
}

// EnvironmentSourceName labels the process environment in JSON output.
const EnvironmentSourceName = "environment"

// SourceFromEnviron turns os.Environ()-style pairs into a Source.
func SourceFromEnviron(environ []string) Source {
	values := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" {
			values[k] = v
		}
	}
	return Source{Name: EnvironmentSourceName, Values: values}
}

// KeyOverrides replaces the default key lists with one name each.
type KeyOverrides struct {
	Registry string
	User     string
	Password string
}

// Credentials is a resolved registry login. Password never reaches JSON.
type Credentials struct {
	Registry    string
	User        string
	Password    string `json:"-"`
	Source      string // where the password was found
	UserKey     string
	PasswordKey string
}

// NoCredentialsError names everything that was searched.
type NoCredentialsError struct {
	Registry     string
	Sources      []string
	UserKeys     []string
	PasswordKeys []string
}

func (e *NoCredentialsError) Error() string {
	target := "the registry"
	if e.Registry != "" {
		target = e.Registry
	}
	return fmt.Sprintf("no registry credentials for %s\n"+
		"  looked in %s for %s\n"+
		"  and %s\n"+
		"hint: confctl set .env %s=<user> %s=<token>\n"+
		"      or pass --user-key / --password-key if the file uses other names",
		target, strings.Join(e.Sources, " and "),
		strings.Join(e.UserKeys, ", "), strings.Join(e.PasswordKeys, ", "),
		e.UserKeys[0], e.PasswordKeys[0])
}

// IsNoCredentials reports whether err means "nothing to log in with".
func IsNoCredentials(err error) bool {
	var nc *NoCredentialsError
	return errors.As(err, &nc)
}

func keysFor(defaults []string, override string) []string {
	if override != "" {
		return []string{override}
	}
	return defaults
}

// lookup returns the first value found: sources in order, keys in order
// within each source, so a project's .env beats the shell environment even
// when the file uses a later key name.
func lookup(keys []string, sources []Source) (value, key, source string, ok bool) {
	for _, s := range sources {
		for _, k := range keys {
			if v, found := s.Values[k]; found && v != "" {
				return v, k, s.Name, true
			}
		}
	}
	return "", "", "", false
}

// Resolve finds a user and a password for registry. An empty registry is
// filled from the registry keys. Missing both is a NoCredentialsError;
// missing exactly one is a plain error naming the gap.
func Resolve(registry string, over KeyOverrides, sources []Source) (*Credentials, error) {
	userKeys := keysFor(UserKeys, over.User)
	passKeys := keysFor(PasswordKeys, over.Password)
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name)
	}
	if registry == "" {
		registry, _, _, _ = lookup(keysFor(RegistryKeys, over.Registry), sources)
	}
	user, userKey, _, hasUser := lookup(userKeys, sources)
	pass, passKey, passSource, hasPass := lookup(passKeys, sources)
	switch {
	case !hasUser && !hasPass:
		return nil, &NoCredentialsError{Registry: registry, Sources: names, UserKeys: userKeys, PasswordKeys: passKeys}
	case !hasUser:
		return nil, fmt.Errorf("found %s but no user: set one of %s (looked in %s)",
			passKey, strings.Join(userKeys, ", "), strings.Join(names, " and "))
	case !hasPass:
		return nil, fmt.Errorf("found %s but no password: set one of %s (looked in %s)",
			userKey, strings.Join(passKeys, ", "), strings.Join(names, " and "))
	}
	return &Credentials{
		Registry:    registry,
		User:        user,
		Password:    pass,
		Source:      passSource,
		UserKey:     userKey,
		PasswordKey: passKey,
	}, nil
}
