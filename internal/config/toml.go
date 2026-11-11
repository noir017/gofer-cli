package config

import (
	"fmt"
	"strconv"
	"strings"
)

// parseTOML reads the flat "key = value" subset of TOML the config file uses.
// Sections, arrays and multi-line strings are rejected rather than ignored.
func parseTOML(text string) (map[string]string, error) {
	values := make(map[string]string)
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return nil, fmt.Errorf("line %d: sections are not supported", i+1)
		}
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", i+1)
		}
		value, err := parseTOMLValue(strings.TrimSpace(rawValue))
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		values[key] = value
	}
	return values, nil
}

// stripTOMLComment drops a trailing # comment, except inside a string.
func stripTOMLComment(line string) string {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		switch quote {
		case '"':
			if r == '\\' {
				escaped = true
			} else if r == '"' {
				quote = 0
			}
		case '\'':
			if r == '\'' {
				quote = 0
			}
		default:
			switch r {
			case '"', '\'':
				quote = r
			case '#':
				return line[:i]
			}
		}
	}
	return line
}

func parseTOMLValue(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("missing value")
	}
	switch raw[0] {
	case '"':
		if strings.HasPrefix(raw, `"""`) {
			return "", fmt.Errorf("multi-line strings are not supported")
		}
		return parseBasicString(raw)
	case '\'':
		if strings.HasPrefix(raw, "'''") {
			return "", fmt.Errorf("multi-line strings are not supported")
		}
		if len(raw) < 2 || raw[len(raw)-1] != '\'' {
			return "", fmt.Errorf("unterminated string")
		}
		inner := raw[1 : len(raw)-1]
		if strings.ContainsRune(inner, '\'') {
			return "", fmt.Errorf("unexpected text after string")
		}
		return inner, nil
	default:
		// Bare values (numbers, booleans) are kept as written.
		if strings.ContainsAny(raw, " \t") {
			return "", fmt.Errorf("unquoted value with spaces")
		}
		return raw, nil
	}
}

func parseBasicString(raw string) (string, error) {
	var b strings.Builder
	for i := 1; i < len(raw); i++ {
		switch raw[i] {
		case '"':
			if strings.TrimSpace(raw[i+1:]) != "" {
				return "", fmt.Errorf("unexpected text after string")
			}
			return b.String(), nil
		case '\\':
			i++
			if i >= len(raw) {
				return "", fmt.Errorf("unterminated escape")
			}
			switch raw[i] {
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
				width := 4
				if raw[i] == 'U' {
					width = 8
				}
				if i+width >= len(raw) {
					return "", fmt.Errorf("short unicode escape")
				}
				code, err := strconv.ParseUint(raw[i+1:i+1+width], 16, 32)
				if err != nil {
					return "", fmt.Errorf("bad unicode escape")
				}
				b.WriteRune(rune(code))
				i += width
			default:
				return "", fmt.Errorf("unknown escape \\%c", raw[i])
			}
		default:
			b.WriteByte(raw[i])
		}
	}
	return "", fmt.Errorf("unterminated string")
}
