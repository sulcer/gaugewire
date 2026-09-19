package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sulcer/gaugewire/internal/config"
)

// ErrNoAPIKey means neither the key file nor the environment holds a key.
var ErrNoAPIKey = errors.New("no Databox API key: set credentials.apiKeyFile or the environment variable")

// loadAPIKey reads the key from the configured file, else from the named
// environment variable. The key is returned to the caller and never logged.
// On Unix a key file readable by others yields a warning the caller may log.
func loadAPIKey(creds config.Credentials, getenv func(string) string) (key, warn string, err error) {
	if creds.APIKeyFile != "" {
		raw, readErr := os.ReadFile(creds.APIKeyFile)
		if readErr != nil {
			return "", "", fmt.Errorf("read key file: %w", readErr)
		}
		key = strings.TrimSpace(string(raw))
		if key == "" {
			return "", "", fmt.Errorf("key file %s is empty", creds.APIKeyFile)
		}
		return key, keyFileWarning(creds.APIKeyFile), nil
	}
	name := creds.APIKeyEnv
	if name == "" {
		name = config.DefaultAPIKeyEnv
	}
	if key = strings.TrimSpace(getenv(name)); key != "" {
		return key, "", nil
	}
	return "", "", ErrNoAPIKey
}
