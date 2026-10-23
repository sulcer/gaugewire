package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sulcer/gaugewire/internal/config"
)

func TestLoadAPIKeyPrefersTheFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(path, []byte("  file-key \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	key, warn, err := loadAPIKey(config.Credentials{APIKeyEnv: "GW_TEST_KEY", APIKeyFile: path}, func(string) string { return "env-key" })
	got := struct {
		key, warn string
		err       bool
	}{key, warn, err != nil}
	want := struct {
		key, warn string
		err       bool
	}{"file-key", "", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadAPIKeyFallsBackToTheEnvironment(t *testing.T) {
	t.Parallel()
	key, warn, err := loadAPIKey(config.Credentials{}, func(name string) string {
		if name == config.DefaultAPIKeyEnv {
			return "env-key"
		}
		return ""
	})
	got := struct {
		key, warn string
		err       bool
	}{key, warn, err != nil}
	want := struct {
		key, warn string
		err       bool
	}{"env-key", "", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadAPIKeyReportsNoKey(t *testing.T) {
	t.Parallel()
	_, _, err := loadAPIKey(config.Credentials{APIKeyEnv: "GW_UNSET"}, func(string) string { return "" })
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("got %v, want ErrNoAPIKey", err)
	}
}

// The path is a key pasted where the path belongs; the error must not echo it.
func TestLoadAPIKeyDoesNotEchoAMissingKeyFilePath(t *testing.T) {
	t.Parallel()
	pasted := filepath.Join(t.TempDir(), "dbx-pasted-key-0123")
	_, _, err := loadAPIKey(config.Credentials{APIKeyFile: pasted}, func(string) string { return "" })
	want := "key file not found; credentials.apiKeyFile and --api-key-file take the path of a file that holds the key"
	if got := fmt.Sprint(err); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoadAPIKeyRejectsAnEmptyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, _, err := loadAPIKey(config.Credentials{APIKeyFile: path}, func(string) string { return "env-key" })
	if err == nil {
		t.Fatal("got nil, want an error for an empty key file")
	}
}

func TestLoadAPIKeyWarnsAboutAWorldReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	t.Parallel()
	path := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(path, []byte("k"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// WriteFile's mode is masked by the umask; chmod is not.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	key, warn, err := loadAPIKey(config.Credentials{APIKeyFile: path}, func(string) string { return "" })
	got := struct {
		key  string
		warn string
		err  bool
	}{key, warn, err != nil}
	want := struct {
		key  string
		warn string
		err  bool
	}{"k", "key file " + path + " is readable by others; use mode 0600", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
