// Package settings edits one top-level member of a JSON object while leaving
// every other byte of the document untouched. Gaugewire uses it to install and
// remove its status-line command in Claude Code's settings file.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotObject means the document's top level is not a JSON object.
var ErrNotObject = errors.New("settings: top level is not a JSON object")

// errTrailingData means the document continues after the top-level object, so
// rewriting it would silently drop whatever follows.
var errTrailingData = errors.New("settings: trailing data after the object")

// Member is the result of Get: the exact value bytes of a top-level member
// and where they sit in the document.
type Member struct {
	Found bool
	Value json.RawMessage

	key        string
	keyStart   int // offset of the key's opening quote
	valueStart int // offset of the value's first byte
	valueEnd   int // offset just past the value's last byte
	prevEnd    int // offset just past the previous value, or past "{" for the first member
}

type layout struct {
	afterBrace int // offset just past "{"
	firstKey   int // offset of the first key's opening quote, or -1
	lastEnd    int // offset just past the last value, or afterBrace when empty
	closeBrace int // offset of the closing "}"
	members    []Member
}

// Get locates key among the top-level members. Value holds the exact source
// bytes of the value, so writing it back reproduces the original document.
func Get(object []byte, key string) (Member, error) {
	l, err := scan(object)
	if err != nil {
		return Member{}, err
	}
	for i, m := range l.members {
		if m.key == key {
			return l.members[i], nil
		}
	}
	return Member{}, nil
}

// Set replaces the member's value in place, or appends the member after the
// last one using the document's own indentation.
func Set(object []byte, key string, value json.RawMessage) ([]byte, error) {
	l, err := scan(object)
	if err != nil {
		return nil, err
	}
	for _, m := range l.members {
		if m.key == key {
			return splice(object, m.valueStart, m.valueEnd, value), nil
		}
	}
	indent := []byte("\n  ")
	if l.firstKey >= 0 {
		indent = object[l.afterBrace:l.firstKey]
	}
	separator := `":`
	if bytes.ContainsRune(indent, '\n') {
		separator = `": `
	}
	member := append([]byte(`"`+key+separator), value...)
	if len(l.members) == 0 {
		insert := append(append(append([]byte{}, indent...), member...), '\n')
		return splice(object, l.afterBrace, l.closeBrace, insert), nil
	}
	insert := append(append([]byte{','}, indent...), member...)
	return splice(object, l.lastEnd, l.lastEnd, insert), nil
}

// Delete removes the member, its value and the punctuation that joined it to
// its neighbours. A document without the member is returned unchanged.
func Delete(object []byte, key string) ([]byte, error) {
	l, err := scan(object)
	if err != nil {
		return nil, err
	}
	for i, m := range l.members {
		if m.key != key {
			continue
		}
		switch {
		case len(l.members) == 1:
			return splice(object, l.afterBrace, l.closeBrace, nil), nil
		case i == len(l.members)-1:
			return splice(object, m.prevEnd, m.valueEnd, nil), nil
		default:
			return splice(object, m.keyStart, l.members[i+1].keyStart, nil), nil
		}
	}
	return object, nil
}

// Load reads the settings file. A missing file reads as an empty object.
func Load(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}"), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read settings: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("{}"), true, nil
	}
	return data, true, nil
}

// DefaultPath is Claude Code's user settings file.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func scan(object []byte) (layout, error) {
	dec := json.NewDecoder(bytes.NewReader(object))
	tok, err := dec.Token()
	if err != nil {
		return layout{}, fmt.Errorf("settings: %w", err)
	}
	if tok != json.Delim('{') {
		return layout{}, ErrNotObject
	}
	l := layout{afterBrace: int(dec.InputOffset()), firstKey: -1}
	l.lastEnd = l.afterBrace
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return layout{}, fmt.Errorf("settings: %w", err)
		}
		name, _ := keyTok.(string)
		keyEnd := int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return layout{}, fmt.Errorf("settings: %w", err)
		}
		m := Member{Found: true, Value: raw, key: name, prevEnd: l.lastEnd}
		m.keyStart = skip(object, l.lastEnd, " \t\r\n,")
		m.valueStart = skip(object, keyEnd, " \t\r\n:")
		m.valueEnd = m.valueStart + len(raw)
		if l.firstKey < 0 {
			l.firstKey = m.keyStart
		}
		l.lastEnd = m.valueEnd
		l.members = append(l.members, m)
	}
	if _, err := dec.Token(); err != nil {
		return layout{}, fmt.Errorf("settings: %w", err)
	}
	l.closeBrace = int(dec.InputOffset()) - 1
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return layout{}, errTrailingData
	}
	return l, nil
}

func skip(object []byte, from int, set string) int {
	for from < len(object) && strings.IndexByte(set, object[from]) >= 0 {
		from++
	}
	return from
}

func splice(object []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(object)-(end-start)+len(replacement))
	out = append(out, object[:start]...)
	out = append(out, replacement...)
	return append(out, object[end:]...)
}
