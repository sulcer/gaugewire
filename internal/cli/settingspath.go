package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// resolveSettingsPath makes a settings path absolute and follows symlinks, so a
// dotfiles-managed settings file is edited in place and its backup lands next to
// the real file. A path that does not exist yet keeps its absolute form.
func resolveSettingsPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve settings path: %w", err)
	}
	if _, statErr := os.Stat(absolute); statErr == nil {
		resolved, evalErr := filepath.EvalSymlinks(absolute)
		if evalErr != nil {
			return "", fmt.Errorf("resolve settings path: %w", evalErr)
		}
		absolute = resolved
	}
	return absolute, nil
}
