package userconfig

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// line is one parsed line of a settings file. Offsets index text, which
// excludes the line terminator.
type line struct {
	number  int
	text    string
	err     error  // the line is not a comment, table header, or key/value pair
	table   string // the header name on a [table] line
	inTable bool   // the line follows a table header; TOML assigns it to that table

	key        string
	valueStart int // where the value begins
	valueEnd   int // where the value ends; for an invalid value, before any comment
	value      string
	isBool     bool
	valueErr   error // the key parsed but its value did not
}

// splitLines splits data into lines that keep their terminators.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := string(data)
	var lines []string
	for text != "" {
		end := strings.IndexByte(text, '\n')
		if end < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:end+1])
		text = text[end+1:]
	}
	return lines
}

func trimTerminator(raw string) string {
	raw = strings.TrimSuffix(raw, "\n")
	return strings.TrimSuffix(raw, "\r")
}

// byteOrderMark may start a file saved by Windows editors.
const byteOrderMark = "\xEF\xBB\xBF"

func parse(data []byte) []line {
	raw := splitLines(data)
	lines := make([]line, 0, len(raw))
	inTable := false
	for i, text := range raw {
		start := 0
		if i == 0 && strings.HasPrefix(text, byteOrderMark) {
			start = len(byteOrderMark)
		}
		parsed := parseLine(trimTerminator(text), start)
		parsed.number = i + 1
		parsed.inTable = inTable
		if parsed.table != "" {
			inTable = true
		}
		lines = append(lines, parsed)
	}
	return lines
}

func skipSpace(text string, i int) int {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	return i
}

func isBareKeyChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

func parseLine(text string, start int) line {
	parsed := line{text: text}
	i := skipSpace(text, start)
	if i == len(text) || text[i] == '#' {
		return parsed
	}
	if text[i] == '[' {
		end := strings.IndexByte(text[i:], ']')
		name := ""
		if end > 0 {
			name = strings.Trim(text[i:i+end], "[] \t")
		}
		if name == "" {
			parsed.err = errors.New("invalid table header")
			return parsed
		}
		parsed.table = name
		return parsed
	}

	switch text[i] {
	case '"', '\'':
		key, end, err := parseString(text, i)
		if err != nil {
			parsed.err = fmt.Errorf("invalid key: %v", err)
			return parsed
		}
		parsed.key, i = key, end
	default:
		end := i
		for end < len(text) && isBareKeyChar(text[end]) {
			end++
		}
		if end == i {
			parsed.err = errors.New("expected key = value")
			return parsed
		}
		parsed.key, i = text[i:end], end
	}
	i = skipSpace(text, i)
	if i < len(text) && text[i] == '.' {
		parsed.key, parsed.err = "", errors.New("dotted keys are not supported")
		return parsed
	}
	if i == len(text) || text[i] != '=' {
		key := parsed.key
		parsed.key, parsed.err = "", fmt.Errorf("expected = after %s", key)
		return parsed
	}
	i = skipSpace(text, i+1)
	parsed.valueStart = i
	parsed.value, parsed.valueEnd, parsed.isBool, parsed.valueErr = parseValue(text, i)
	if parsed.valueErr == nil {
		if rest := skipSpace(text, parsed.valueEnd); rest < len(text) && text[rest] != '#' {
			parsed.valueErr = errors.New("unexpected text after the value")
		}
	}
	if parsed.valueErr != nil {
		parsed.valueEnd = invalidValueEnd(text, i)
	}
	return parsed
}

// invalidValueEnd finds where an unparsable value ends: before a trailing
// comment when there appears to be one.
func invalidValueEnd(text string, start int) int {
	end := len(text)
	if hash := strings.IndexByte(text[start:], '#'); hash >= 0 {
		end = start + hash
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t') {
		end--
	}
	return end
}

func parseValue(text string, i int) (value string, end int, isBool bool, err error) {
	if i == len(text) || text[i] == '#' {
		return "", i, false, errors.New("missing value")
	}
	rest := text[i:]
	if strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, `'''`) {
		return "", i, false, errors.New("multi-line strings are not supported")
	}
	if text[i] == '"' || text[i] == '\'' {
		value, end, err = parseString(text, i)
		return value, end, false, err
	}
	for _, word := range []string{"true", "false"} {
		if strings.HasPrefix(rest, word) {
			end := i + len(word)
			if end == len(text) || !isBareKeyChar(text[end]) {
				return word, end, true, nil
			}
		}
	}
	return "", i, false, errors.New(`value must be a quoted string ("...") or true or false`)
}

// parseString parses a TOML basic ("...") or literal ('...') string starting
// at the opening quote and returns its value and the offset after it.
func parseString(text string, i int) (string, int, error) {
	quote := text[i]
	var b strings.Builder
	for j := i + 1; j < len(text); j++ {
		c := text[j]
		switch {
		case c == quote:
			value := b.String()
			if !utf8.ValidString(value) {
				return "", 0, errors.New("string is not valid UTF-8")
			}
			return value, j + 1, nil
		case c < 0x20 && c != '\t' || c == 0x7f:
			return "", 0, errors.New("control characters must be escaped")
		case c == '\\' && quote == '"':
			if j+1 >= len(text) {
				return "", 0, errors.New("unterminated string")
			}
			j++
			switch text[j] {
			case 'b':
				b.WriteByte('\b')
			case 't':
				b.WriteByte('\t')
			case 'n':
				b.WriteByte('\n')
			case 'f':
				b.WriteByte('\f')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'u', 'U':
				digits := 4
				if text[j] == 'U' {
					digits = 8
				}
				if j+digits >= len(text) {
					return "", 0, errors.New("incomplete unicode escape")
				}
				code, err := strconv.ParseUint(text[j+1:j+1+digits], 16, 32)
				if err != nil || !utf8.ValidRune(rune(code)) {
					return "", 0, fmt.Errorf("invalid unicode escape \\%s", text[j:j+1+digits])
				}
				b.WriteRune(rune(code))
				j += digits
			default:
				return "", 0, fmt.Errorf("invalid escape \\%c", text[j])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, errors.New("unterminated string")
}

// quote formats value as a TOML basic string.
func quote(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
