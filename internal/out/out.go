package out

import (
	"encoding/json"
	"fmt"
	"os"
)

// JSON encodes v to stdout and returns a non-zero process exit code on encode error.
func JSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "devrig: encode json: %v\n", err)
		os.Exit(1)
	}
}

// Fatal prints err to stderr and exits 1.
func Fatal(err error) {
	fmt.Fprintf(os.Stderr, "devrig: %v\n", err)
	os.Exit(1)
}

// Fatalf formats and exits 1.
func Fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "devrig: "+format+"\n", args...)
	os.Exit(1)
}
