// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import "strings"

const diffHeaderPrefix = "diff --git "

// parseDiffHeader extracts the old and new paths from a "diff --git" header
// line, with the "a/" and "b/" prefixes removed. ok is false when line is not
// such a header.
//
// Two header shapes need more than a naive "a/(.+?) b/(.+)" split:
//
//   - Git C-quotes a path containing '"', '\' or a control character, even
//     under core.quotepath=false: diff --git "a/b\"q.go" "b/b\"q.go". Each
//     side is quoted independently, so a rename can mix quoted and bare
//     tokens.
//   - An unquoted path may itself contain " b/" (e.g. "x b/y.go"). For a
//     non-rename both sides name the same path, so the header is exactly
//     "a/P b/P" and the split is the one where both halves are equal. A
//     rename whose paths contain " b/" stays ambiguous here; its "rename
//     from"/"rename to" lines carry the authoritative paths.
func parseDiffHeader(line string) (oldPath, newPath string, ok bool) {
	rest, found := strings.CutPrefix(line, diffHeaderPrefix)
	if !found {
		return "", "", false
	}

	var oldTok, newTok string
	switch {
	case strings.HasPrefix(rest, `"`):
		tok, tail, qok := cutQuoted(rest)
		if !qok {
			return "", "", false
		}
		tail, found = strings.CutPrefix(tail, " ")
		if !found {
			return "", "", false
		}
		oldTok = tok
		if strings.HasPrefix(tail, `"`) {
			tok, tail, qok = cutQuoted(tail)
			if !qok || tail != "" {
				return "", "", false
			}
			newTok = tok
		} else {
			newTok = tail
		}
	case strings.HasSuffix(rest, `"`) && strings.Contains(rest, ` "b/`):
		// Bare old path, quoted new path. A bare path never contains '"'
		// (git would have quoted it), so the first ` "b/` is the boundary.
		i := strings.Index(rest, ` "b/`)
		tok, tail, qok := cutQuoted(rest[i+1:])
		if !qok || tail != "" {
			return "", "", false
		}
		oldTok, newTok = rest[:i], tok
	default:
		var sok bool
		oldTok, newTok, sok = splitBareHeader(rest)
		if !sok {
			return "", "", false
		}
	}

	oldPath, found = strings.CutPrefix(oldTok, "a/")
	if !found || oldPath == "" {
		return "", "", false
	}
	newPath, found = strings.CutPrefix(newTok, "b/")
	if !found || newPath == "" {
		return "", "", false
	}
	return oldPath, newPath, true
}

// splitBareHeader splits an unquoted "a/OLD b/NEW" header body. It prefers
// the split where OLD == NEW (the non-rename case, which is unambiguous even
// when the path contains " b/") and otherwise falls back to the first " b/".
func splitBareHeader(rest string) (oldTok, newTok string, ok bool) {
	if len(rest) < 3 || !strings.HasPrefix(rest, "a/") {
		return "", "", false
	}
	// "a/" + P + " b/" + P has length 2*len(P) + 5.
	if n := len(rest) - 5; n > 0 && n%2 == 0 {
		p := n / 2
		if rest[2+p:5+p] == " b/" && rest[2:2+p] == rest[5+p:] {
			return rest[:2+p], rest[3+p:], true
		}
	}
	// First " b/" that leaves a non-empty old path.
	i := strings.Index(rest[3:], " b/")
	if i < 0 {
		return "", "", false
	}
	i += 3
	return rest[:i], rest[i+1:], true
}

// unquoteGitPath decodes a path as git prints it in extended headers such as
// "rename from": C-quoted when it contains special characters, bare
// otherwise. A value that does not parse as a quoted string is returned
// unchanged.
func unquoteGitPath(s string) string {
	if !strings.HasPrefix(s, `"`) {
		return s
	}
	if p, tail, ok := cutQuoted(s); ok && tail == "" {
		return p
	}
	return s
}

// cutQuoted decodes the C-style quoted string at the start of s (git's
// quote_c_style: \a \b \t \n \v \f \r \" \\ and three-digit octal escapes for
// other bytes) and returns it with the text after the closing quote.
func cutQuoted(s string) (unquoted, tail string, ok bool) {
	if !strings.HasPrefix(s, `"`) {
		return "", s, false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			return b.String(), s[i+1:], true
		case '\\':
			i++
			if i >= len(s) {
				return "", s, false
			}
			switch e := s[i]; e {
			case 'a':
				b.WriteByte('\a')
			case 'b':
				b.WriteByte('\b')
			case 't':
				b.WriteByte('\t')
			case 'n':
				b.WriteByte('\n')
			case 'v':
				b.WriteByte('\v')
			case 'f':
				b.WriteByte('\f')
			case 'r':
				b.WriteByte('\r')
			case '"', '\\':
				b.WriteByte(e)
			case '0', '1', '2', '3':
				if i+2 >= len(s) || !isOctal(s[i+1]) || !isOctal(s[i+2]) {
					return "", s, false
				}
				b.WriteByte((e-'0')<<6 | (s[i+1]-'0')<<3 | (s[i+2] - '0'))
				i += 2
			default:
				return "", s, false
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", s, false
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
