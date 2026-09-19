//go:build !windows

package cli

import "os"

// keyFileWarning reports that the key file is readable beyond its owner, so
// the caller can say so without refusing to start.
func keyFileWarning(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return "key file " + path + " is readable by others; use mode 0600"
}
