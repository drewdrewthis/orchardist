package features_test

import (
	"io"
	"os"
)

// appendFileOS opens path for appending, creating it if absent.
// Lives in its own file to make the O_APPEND | O_CREATE | O_WRONLY flags visible.
func appendFileOS(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
