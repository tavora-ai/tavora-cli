// Package jsonc decodes JSON-with-comments — JSON augmented with
// `//` line comments, `/* */` block comments, and trailing commas.
//
// The strategy is a single-pass scanner that rewrites comment and
// trailing-comma bytes into spaces (preserving newlines and total
// byte length) before handing the result to encoding/json. Keeping
// byte offsets stable means a json.SyntaxError pointing at offset N
// in the cleaned stream still points at line/column N in the
// original source — line numbers in error messages stay honest.
package jsonc

import (
	"encoding/json"
	"fmt"
)

// Strip rewrites comments and trailing commas in src to whitespace
// (newlines preserved), returning a buffer the same length as src.
// It also tracks whether the result still parses as JSON; callers
// who need richer error reporting should use Unmarshal.
func Strip(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)

	i := 0
	n := len(out)
	for i < n {
		c := out[i]
		switch c {
		case '"':
			// Skip string literal — preserve contents untouched
			i++
			for i < n {
				if out[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if out[i] == '"' {
					i++
					break
				}
				i++
			}
		case '/':
			if i+1 < n && out[i+1] == '/' {
				// Line comment — blank to end of line
				out[i] = ' '
				out[i+1] = ' '
				i += 2
				for i < n && out[i] != '\n' {
					out[i] = ' '
					i++
				}
				// keep '\n' as-is
			} else if i+1 < n && out[i+1] == '*' {
				// Block comment — blank inside, keep newlines
				out[i] = ' '
				out[i+1] = ' '
				i += 2
				for i < n {
					if out[i] == '*' && i+1 < n && out[i+1] == '/' {
						out[i] = ' '
						out[i+1] = ' '
						i += 2
						break
					}
					if out[i] != '\n' {
						out[i] = ' '
					}
					i++
				}
			} else {
				i++
			}
		case ',':
			// Look ahead for trailing comma before } or ]
			j := i + 1
			for j < n && isJSONSpace(out[j]) {
				j++
			}
			if j < n && (out[j] == ']' || out[j] == '}') {
				out[i] = ' '
			}
			i++
		default:
			i++
		}
	}

	return out
}

func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// Unmarshal decodes JSONC into v. On decode failure, the returned
// error includes a 1-based line and column derived from the byte
// offset reported by encoding/json.
func Unmarshal(src []byte, v any) error {
	cleaned := Strip(src)
	if err := json.Unmarshal(cleaned, v); err != nil {
		if syn, ok := err.(*json.SyntaxError); ok {
			line, col := lineCol(src, int(syn.Offset))
			return fmt.Errorf("line %d:%d: %s", line, col, syn.Error())
		}
		if ute, ok := err.(*json.UnmarshalTypeError); ok {
			line, col := lineCol(src, int(ute.Offset))
			where := ute.Field
			if where == "" {
				where = ute.Type.String()
			}
			return fmt.Errorf("line %d:%d: %s expected %s", line, col, where, ute.Type)
		}
		return err
	}
	return nil
}

// lineCol returns the 1-based line and column for a byte offset in
// src. Offsets past the end clamp to the final position.
func lineCol(src []byte, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	line := 1
	col := 1
	for i := 0; i < offset; i++ {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}
