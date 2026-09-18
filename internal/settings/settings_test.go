package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var installed = json.RawMessage(`{"type":"command","command":"/opt/gaugewire statusline"}`)

var fixtures = []string{"empty", "none", "only", "first", "middle", "last", "compact"}

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func TestSetReplacesOrAppendsOnlyTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Set(read(t, name+".json"), "statusLine", installed)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if diff := cmp.Diff(string(read(t, name+".set.golden")), string(got)); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDeleteRemovesOnlyTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Delete(read(t, name+".json"), "statusLine")
			if err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if diff := cmp.Diff(string(read(t, name+".delete.golden")), string(got)); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetThenDeleteRestoresAFileWithoutTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"empty", "none"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			original := read(t, name+".json")
			set, err := Set(original, "statusLine", installed)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := Delete(set, "statusLine")
			if err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if diff := cmp.Diff(string(original), string(got)); diff != "" {
				t.Fatalf("round trip changed bytes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetWithTheOriginalValueRestoresAFileWithTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"only", "first", "middle", "last", "compact"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			original := read(t, name+".json")
			member, err := Get(original, "statusLine")
			if err != nil || !member.Found {
				t.Fatalf("Get: found=%v err=%v", member.Found, err)
			}
			set, err := Set(original, "statusLine", installed)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := Set(set, "statusLine", member.Value)
			if err != nil {
				t.Fatalf("Set back: %v", err)
			}
			if diff := cmp.Diff(string(original), string(got)); diff != "" {
				t.Fatalf("round trip changed bytes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetAppendsWithoutASpaceWhenTheObjectHasNoIndent(t *testing.T) {
	t.Parallel()
	got, err := Set([]byte(`{"model":"claude-sonnet-5"}`), "statusLine", installed)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	want := `{"model":"claude-sonnet-5","statusLine":{"type":"command","command":"/opt/gaugewire statusline"}}`
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestGetReturnsTheExactValueBytes(t *testing.T) {
	t.Parallel()
	got, err := Get(read(t, "only.json"), "statusLine")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Member{Found: true, Value: json.RawMessage(`{ "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }`)}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(Member{}), cmp.Comparer(func(a, b json.RawMessage) bool { return string(a) == string(b) })); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestGetOnAMissingMember(t *testing.T) {
	t.Parallel()
	member, err := Get(read(t, "none.json"), "statusLine")
	type outcome struct {
		found bool
		value string
		err   bool
	}
	got := outcome{member.Found, string(member.Value), err != nil}
	want := outcome{false, "", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestOperationsRejectANonObject(t *testing.T) {
	t.Parallel()
	_, err := Get([]byte(`["statusLine"]`), "statusLine")
	if !errors.Is(err, ErrNotObject) {
		t.Fatalf("got %v, want ErrNotObject", err)
	}
}

func TestOperationsReportMalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := Set([]byte(`{"statusLine": `), "statusLine", installed)
	if err == nil || errors.Is(err, ErrNotObject) {
		t.Fatalf("got %v, want a decode error", err)
	}
}

func TestOperationsRejectTrailingData(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"garbage after the object", `{"a":1} oops`},
		{"a second object", `{"a":1}{"b":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Get([]byte(tc.input), "a")
			type outcome struct {
				failed      bool
				notAnObject bool
			}
			got := outcome{err != nil, errors.Is(err, ErrNotObject)}
			want := outcome{true, false}
			if got != want {
				t.Fatalf("got %+v (err %v), want %+v", got, err, want)
			}
		})
	}
}

func TestLoadTreatsAnEmptyFileAsAnEmptyObject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"no bytes", ""},
		{"whitespace only", " \n\t "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			data, existed, err := Load(path)
			type outcome struct {
				data    string
				existed bool
				err     bool
			}
			got := outcome{string(data), existed, err != nil}
			want := outcome{"{}", true, false}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestLoadTreatsAMissingFileAsAnEmptyObject(t *testing.T) {
	t.Parallel()
	data, existed, err := Load(filepath.Join(t.TempDir(), "settings.json"))
	type outcome struct {
		data    string
		existed bool
		err     bool
	}
	got := outcome{string(data), existed, err != nil}
	want := outcome{"{}", false, false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDefaultPathIsUnderTheUserHome(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home")
	}
	path, err := DefaultPath()
	type outcome struct {
		path string
		err  bool
	}
	got := outcome{path, err != nil}
	want := outcome{filepath.Join(home, ".claude", "settings.json"), false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
