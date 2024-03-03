package lyml

import (
	"strings"

	"github.com/pme-sh/pmesh/luae"

	lua "github.com/yuin/gopher-lua"
)

// BalancedBrackets returns the body of the first balanced pair of brackets in s, and the rest of s after the closing bracket.
func balancedBrackets(s string, b, e rune) (body string, rest string, ok bool) {
	count := 1
	for i, c := range s {
		if c == b {
			count++
		} else if c == e {
			count--
		}
		if count == 0 {
			return s[:i], s[i+1:], true
		}
	}
	return "", s, false
}

// EscapeSubstitute replaces all occurrences of $(...) in s with the result of lookup(body).
func escapeSubstitute(s string, lookup func(string) (string, error)) (result string) {
	for {
		i := strings.Index(s, "$(")
		if i == -1 {
			return result + s
		}
		result += s[:i]

		body, rest, ok := balancedBrackets(s[i+2:], '(', ')')
		if !ok {
			return result + s
		}
		eval, err := lookup(body)
		if err != nil {
			result += "$(" + body + ")"
		} else {
			result += eval
		}
		s = rest
	}
}

// EvalEscape evaluates all occurrences of $(...) in s as Lua expressions.
func evalEscape(s string, state *lua.LState) (result string) {
	return escapeSubstitute(s, func(s string) (res string, err error) {
		err = luae.EvalLua(state, s, &res)
		return
	})
}
