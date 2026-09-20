package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// resolveSettingsPath makes a settings path absolute and follows symlinks, so a
// dotfiles-managed file is edited in place and its backup lands beside it. A
// file that does not exist yet resolves through its parent directory.
func resolveSettingsPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve settings path: %w", err)
	}
	_, statErr := os.Stat(absolute)
	switch {
	case statErr == nil:
		resolved, evalErr := filepath.EvalSymlinks(absolute)
		if evalErr != nil {
			return "", fmt.Errorf("resolve settings path: %w", evalErr)
		}
		return resolved, nil
	case errors.Is(statErr, os.ErrNotExist):
		if dir, evalErr := filepath.EvalSymlinks(filepath.Dir(absolute)); evalErr == nil {
			return filepath.Join(dir, filepath.Base(absolute)), nil
		}
		return absolute, nil
	default:
		return "", fmt.Errorf("resolve settings path: %w", statErr)
	}
}
