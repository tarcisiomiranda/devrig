package image

import (
	"fmt"

	"github.com/distribution/reference"
)

// Registry derives the registry host from an image reference the way the
// tools do: "org/app:tag" and "app" live on docker.io.
func Registry(ref string) (string, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", ref, err)
	}
	return reference.Domain(named), nil
}
