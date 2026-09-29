package config

// stripJSONC removes // line comments from a JSON document before parsing,
// so config files can carry explanatory comments (JSONC style). Block
// comments are not supported.
//
// Each comment is replaced with spaces up to — but not including — the line's
// newline, so the output keeps the exact length and line structure of the
// input. JSON parse errors therefore report byte offsets and line positions
// that match the original file.
//
// // inside a JSON string is left alone: "https://…" is a value, not a
// comment. Strings are tracked with a quote-and-escape state machine, so
// \" does not close a string and \\\\ does not open one.
//
// The returned error is reserved and is currently always nil; callers wrap
// it in the same defer-and-check style as the parse that follows.
func stripJSONC(data []byte) ([]byte, error) {
	out := make([]byte, len(data))
	copy(out, data)
	inString := false
	for i := 0; i < len(out); i++ {
		switch c := out[i]; {
		case inString && c == '\\':
			i++ // skip the escaped character
		case inString && c == '"':
			inString = false
		case !inString && c == '"':
			inString = true
		case !inString && c == '/' && i+1 < len(out) && out[i+1] == '/':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
			// i sits on the newline (or end of input); the newline itself
			// is left intact so line numbering survives.
		}
	}
	return out, nil
}
