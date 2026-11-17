// Package gitignore matches repository-relative paths against gitignore rules.
//
// It implements the pattern syntax documented in gitignore(5): comments,
// escaped leading characters, negation, directory-only patterns, anchoring,
// and the "**" forms. Rule sources are layered by the caller in increasing
// precedence; the last matching rule decides.
package gitignore

import (
	"path"
	"strings"
)

// Rule is one parsed pattern line.
type Rule struct {
	// base is the repository-relative directory holding the rule source, using
	// forward slashes. It is empty for the repository root.
	base     string
	segments []string
	// foldedBase and folded are lower-cased copies for case-insensitive matching.
	foldedBase string
	folded     []string
	negate     bool
	dirOnly    bool
	// basename rules contain no slash and match the final path element at any
	// depth below base.
	basename bool
}

// Parse reads gitignore lines whose source lives in the repository-relative
// directory base ("" for the repository root, slash-separated otherwise).
func Parse(data []byte, base string) []Rule {
	base = strings.Trim(base, "/")
	var rules []Rule
	for _, line := range strings.Split(string(data), "\n") {
		if rule, ok := parseLine(strings.TrimSuffix(line, "\r"), base); ok {
			rules = append(rules, rule.withFolded())
		}
	}
	return rules
}

func parseLine(line, base string) (Rule, bool) {
	line = trimTrailingSpaces(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Rule{}, false
	}
	rule := Rule{base: base}
	switch {
	case strings.HasPrefix(line, "!"):
		rule.negate = true
		line = line[1:]
	case strings.HasPrefix(line, `\!`), strings.HasPrefix(line, `\#`):
		line = line[1:]
	}
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimRight(line, "/")
	}
	if line == "" {
		return Rule{}, false
	}
	if !strings.Contains(line, "/") {
		rule.basename = true
		rule.segments = []string{convertClass(line)}
		return rule, true
	}
	line = strings.TrimPrefix(line, "/")
	for _, segment := range strings.Split(line, "/") {
		if segment == "" {
			continue
		}
		rule.segments = append(rule.segments, convertClass(segment))
	}
	return rule, len(rule.segments) > 0
}

func (r Rule) withFolded() Rule {
	r.foldedBase = strings.ToLower(r.base)
	r.folded = make([]string, len(r.segments))
	for i, segment := range r.segments {
		r.folded[i] = strings.ToLower(segment)
	}
	return r
}

// trimTrailingSpaces removes unescaped trailing spaces, as git does.
func trimTrailingSpaces(line string) string {
	for strings.HasSuffix(line, " ") {
		trimmed := line[:len(line)-1]
		backslashes := 0
		for i := len(trimmed) - 1; i >= 0 && trimmed[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			// The final space is escaped; drop the escape and keep the space.
			return trimmed[:len(trimmed)-1] + " "
		}
		line = trimmed
	}
	return line
}

// convertClass rewrites git's "[!...]" negated class to path.Match's "[^...]".
func convertClass(segment string) string {
	return strings.ReplaceAll(segment, "[!", "[^")
}

// match reports whether the rule's pattern matches rel, a repository-relative
// slash path. With fold, rel is already lower-cased and the rule's lower-cased
// pattern is used. Negation is applied by Ignored.
func (r Rule) match(rel string, isDir, fold bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	base, segments := r.base, r.segments
	if fold {
		base, segments = r.foldedBase, r.folded
	}
	local := rel
	if base != "" {
		if !strings.HasPrefix(rel, base+"/") {
			return false
		}
		local = rel[len(base)+1:]
	}
	if r.basename {
		ok, _ := path.Match(segments[0], path.Base(local))
		return ok
	}
	return matchSegments(segments, strings.Split(local, "/"), isDir)
}

func matchSegments(pattern, parts []string, isDir bool) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			rest := pattern[1:]
			if len(rest) == 0 {
				// A trailing "/**" matches everything inside. Like git, it also
				// matches the directory itself, but not a file of that name.
				return len(parts) > 0 || isDir
			}
			for i := 0; i <= len(parts); i++ {
				if matchSegments(rest, parts[i:], isDir) {
					return true
				}
			}
			return false
		}
		if len(parts) == 0 {
			return false
		}
		if ok, _ := path.Match(pattern[0], parts[0]); !ok {
			return false
		}
		pattern, parts = pattern[1:], parts[1:]
	}
	return len(parts) == 0
}

// Ignored reports whether rel is ignored by rules ordered from lowest to
// highest precedence. The last matching rule decides, so a later negation
// re-includes a path that an earlier rule excluded. ignoreCase matches the way
// git does when core.ignoreCase is set, the default on Windows and macOS.
func Ignored(rules []Rule, rel string, isDir, ignoreCase bool) bool {
	rel = strings.Trim(rel, "/")
	if ignoreCase {
		rel = strings.ToLower(rel)
	}
	for i := len(rules) - 1; i >= 0; i-- {
		if rules[i].match(rel, isDir, ignoreCase) {
			return !rules[i].negate
		}
	}
	return false
}
