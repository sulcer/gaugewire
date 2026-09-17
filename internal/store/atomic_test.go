package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicReplacesTheContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v; want \"two\", nil", got, err)
	}
}

func TestWriteFileAtomicLeavesNoTemporaryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := WriteFileAtomic(filepath.Join(dir, "state.json"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "state.json" {
		t.Fatalf("directory holds %v, want [state.json]", names)
	}
}

func TestWriteFileAtomicFailsWhenTheDirectoryIsMissing(t *testing.T) {
	t.Parallel()
	err := WriteFileAtomic(filepath.Join(t.TempDir(), "missing", "state.json"), []byte("x"), 0o600)
	if err == nil {
		t.Fatal("got nil error, want a failure for a missing directory")
	}
}
