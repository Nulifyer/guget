package userconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulifyer/guget/internal/atomicfile"
)

// Set validates value and stores it for key in the file at path. Only the
// key's own line changes: comments, other lines, and a trailing comment on
// that line are kept, and earlier duplicate lines for the key are removed. A
// new key is added before the first table header, or at the end. A missing
// file is created with mode 0600 and a missing directory with mode 0700.
func Set(path string, key Key, value string) error {
	normalized, err := key.Parse(value)
	if err != nil {
		return err
	}
	formatted := normalized
	if key.Kind == String {
		formatted = quote(normalized)
	}
	return edit(path, func(data []byte) (string, bool) {
		return setLine(data, key.Name, formatted), true
	})
}

// Unset removes every line that sets key in the file at path, keeping all
// other lines. A missing file or key is not an error.
func Unset(path string, key Key) error {
	return edit(path, func(data []byte) (string, bool) {
		return unsetLine(data, key.Name)
	})
}

func edit(path string, change func([]byte) (string, bool)) error {
	if path == "" {
		return errors.New("settings file path is empty")
	}
	// Write through symbolic links so dotfile managers keep their link.
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	updated, changed := change(data)
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	return atomicfile.WriteFile(target, []byte(updated), 0o600)
}

// rootMatches returns the indexes of lines that set name outside any table.
func rootMatches(lines []line, name string) []int {
	var matches []int
	for i, parsed := range lines {
		if !parsed.inTable && parsed.err == nil && parsed.table == "" && parsed.key == name {
			matches = append(matches, i)
		}
	}
	return matches
}

func newlineOf(data []byte) string {
	if strings.Contains(string(data), "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func setLine(data []byte, name, formatted string) string {
	raw := splitLines(data)
	lines := parse(data)
	matches := rootMatches(lines, name)
	if len(matches) > 0 {
		last := matches[len(matches)-1]
		parsed := lines[last]
		text := parsed.text
		before, after := "", ""
		if parsed.valueStart == parsed.valueEnd && text[parsed.valueStart-1] == '=' {
			before = " " // the value was missing: `key =`
		}
		if parsed.valueEnd < len(text) && text[parsed.valueEnd] == '#' {
			after = " "
		}
		raw[last] = text[:parsed.valueStart] + before + formatted + after + text[parsed.valueEnd:] + raw[last][len(text):]
		for _, index := range matches[:len(matches)-1] {
			raw[index] = ""
		}
		return strings.Join(raw, "")
	}

	newline := newlineOf(data)
	entry := name + " = " + formatted + newline
	for i, parsed := range lines {
		if parsed.table != "" {
			raw = append(raw[:i], append([]string{entry}, raw[i:]...)...)
			return strings.Join(raw, "")
		}
	}
	text := strings.Join(raw, "")
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += newline
	}
	return text + entry
}

func unsetLine(data []byte, name string) (string, bool) {
	raw := splitLines(data)
	matches := rootMatches(parse(data), name)
	if len(matches) == 0 {
		return "", false
	}
	for _, index := range matches {
		raw[index] = ""
	}
	return strings.Join(raw, ""), true
}
