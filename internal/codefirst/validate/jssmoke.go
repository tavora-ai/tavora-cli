package validate

import "os"

// readAll is a tiny helper kept private so we don't drag the loader
// into validate. It exists only so the unit test can monkey-patch
// the body if needed.
func readAll(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// balancedJSSmoke is a very small balanced-bracket sanity check.
// It correctly steps over string and template literals and line/
// block comments. It will miss many real syntax errors (mismatched
// regex literals, ASI traps) — the server runtime is the
// authoritative parser. This is just a fast-path "you obviously
// broke something" filter for the dev loop.
func balancedJSSmoke(src []byte) error {
	stack := make([]byte, 0, 32)
	push := func(b byte) { stack = append(stack, b) }
	pop := func() (byte, bool) {
		if len(stack) == 0 {
			return 0, false
		}
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return b, true
	}

	n := len(src)
	i := 0
	for i < n {
		c := src[i]
		switch c {
		case '/':
			if i+1 < n && src[i+1] == '/' {
				for i < n && src[i] != '\n' {
					i++
				}
				continue
			}
			if i+1 < n && src[i+1] == '*' {
				i += 2
				for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i += 2
				continue
			}
			i++
		case '"', '\'', '`':
			quote := c
			i++
			for i < n && src[i] != quote {
				if src[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if quote == '`' && src[i] == '$' && i+1 < n && src[i+1] == '{' {
					// template literal interpolation; recurse via the
					// outer brace stack
					push('}')
					i += 2
					continue
				}
				i++
			}
			i++
		case '{':
			push('}')
			i++
		case '[':
			push(']')
			i++
		case '(':
			push(')')
			i++
		case '}', ']', ')':
			top, ok := pop()
			if !ok || top != c {
				return jsSmokeError(src, i, c)
			}
			i++
		default:
			i++
		}
	}
	if len(stack) != 0 {
		return jsSmokeError(src, n, stack[len(stack)-1])
	}
	return nil
}

type jsSmokeErr struct {
	line, col int
	expected  byte
	src       []byte
	offset    int
}

func (e jsSmokeErr) Error() string {
	if e.offset >= len(e.src) {
		return formatSmoke(e.line, e.col, "unexpected end of file (expected '"+string(e.expected)+"')")
	}
	return formatSmoke(e.line, e.col, "unmatched '"+string(e.src[e.offset])+"' (looking for '"+string(e.expected)+"')")
}

func jsSmokeError(src []byte, offset int, expected byte) error {
	l, c := lineColOf(src, offset)
	return jsSmokeErr{line: l, col: c, expected: expected, src: src, offset: offset}
}

func lineColOf(src []byte, offset int) (int, int) {
	if offset > len(src) {
		offset = len(src)
	}
	line, col := 1, 1
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

func formatSmoke(line, col int, msg string) string {
	return "line " + itoa(line) + ":" + itoa(col) + ": " + msg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
