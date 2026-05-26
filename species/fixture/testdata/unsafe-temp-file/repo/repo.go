// Package unsafetempfile is a hermetic fixture for the unsafe-temp-file species:
// WriteCache writes data to a hardcoded, predictable temp path under the system
// temp directory — a symlink/TOCTOU and clobber risk. The detector nominates the
// literal; the recorded fix switches to os.CreateTemp so the OS picks an
// unpredictable name with safe (0600) permissions.
package unsafetempfile

import (
	"os"
)

// WriteCache writes data to a temporary file and returns its path.
func WriteCache(data []byte) (string, error) {
	path := "/tmp/app-cache.tmp"
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}
