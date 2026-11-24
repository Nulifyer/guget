package msbuildstatic

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// value is an evaluated property value. Known is false when the text depends
// on something the static evaluator cannot see: an SDK-defined or global
// property, an unsupported property function, or an unevaluated condition.
// Unresolved references stay in Text literally so callers can show them.
type value struct {
	Text  string
	Known bool
}

// scope is what an expression can see: the properties defined so far, the
// file declaring the expression, and the project being evaluated.
type scope struct {
	eval     *evaluator
	thisFile string // absolute path of the declaring file
}

// expand replaces $(Name) references and the supported property functions in
// s. Item (@()) and metadata (%()) references cannot be expanded statically and
// make the result unknown.
func (s scope) expand(raw string) value {
	if !strings.ContainsAny(raw, "$@%") {
		return value{Text: raw, Known: true}
	}
	var out strings.Builder
	known := true
	for i := 0; i < len(raw); {
		c := raw[i]
		if (c == '$' || c == '@' || c == '%') && i+1 < len(raw) && raw[i+1] == '(' {
			end := matchParen(raw, i+1)
			if end < 0 {
				out.WriteString(raw[i:])
				known = false
				break
			}
			inner := raw[i+2 : end]
			if c != '$' {
				out.WriteString(raw[i : end+1])
				known = false
			} else if v := s.evaluateReference(inner); v.Known {
				out.WriteString(v.Text)
			} else {
				if v.Text != "" {
					out.WriteString(v.Text)
				} else {
					out.WriteString(raw[i : end+1])
				}
				known = false
			}
			i = end + 1
			continue
		}
		out.WriteByte(c)
		i++
	}
	return value{Text: out.String(), Known: known}
}

// matchParen returns the index of the parenthesis closing the one at open,
// skipping quoted strings, or -1.
func matchParen(s string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isPropertyName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// evaluateReference evaluates the inside of $(...). An unknown result keeps an
// empty Text so the caller writes the original reference.
func (s scope) evaluateReference(inner string) value {
	inner = strings.TrimSpace(inner)
	if strings.HasPrefix(inner, "[") {
		return s.callStatic(inner)
	}
	if !isPropertyName(inner) {
		return value{} // instance methods such as $(Name.Trim()) are not modeled
	}
	return s.property(inner)
}

func (s scope) property(name string) value {
	if v, ok := s.reserved(name); ok {
		return value{Text: v, Known: true}
	}
	if s.eval.strict {
		return value{}
	}
	if p, ok := s.eval.props[strings.ToLower(name)]; ok {
		if p.Known {
			return value{Text: p.Text, Known: true}
		}
		return value{}
	}
	if v, ok := s.eval.lookupEnv(name); ok {
		return value{Text: v, Known: true}
	}
	if defaultEmpty[strings.ToLower(name)] {
		return value{Text: "", Known: true}
	}
	return value{}
}

// reserved returns MSBuild's reserved properties that are known statically.
func (s scope) reserved(name string) (string, bool) {
	project := s.eval.projectPath
	switch strings.ToLower(name) {
	case "msbuildprojectdirectory":
		return filepath.Dir(project), true
	case "msbuildprojectfullpath":
		return project, true
	case "msbuildprojectfile":
		return filepath.Base(project), true
	case "msbuildprojectname":
		return strings.TrimSuffix(filepath.Base(project), filepath.Ext(project)), true
	case "msbuildprojectextension":
		return filepath.Ext(project), true
	case "msbuildthisfiledirectory":
		return withTrailingSeparator(filepath.Dir(s.thisFile)), true
	case "msbuildthisfile":
		return filepath.Base(s.thisFile), true
	case "msbuildthisfilefullpath":
		return s.thisFile, true
	case "msbuildthisfilename":
		return strings.TrimSuffix(filepath.Base(s.thisFile), filepath.Ext(s.thisFile)), true
	case "msbuildthisfileextension":
		return filepath.Ext(s.thisFile), true
	}
	return "", false
}

// defaultEmpty lists properties that no SDK file defines before the points
// where NuGet and the common props/targets read them, so an undefined value is
// empty rather than unknown. Every other undefined property is unknown because
// an SDK import the static evaluator does not read might define it.
var defaultEmpty = map[string]bool{}

func init() {
	for _, name := range []string{
		"ManagePackageVersionsCentrally", "CentralPackageFloatingVersionsEnabled", "CentralPackageVersionOverrideEnabled",
		"CentralPackageTransitivePinningEnabled", "RestoreEnableGlobalPackageReference",
		"ImportDirectoryBuildProps", "DirectoryBuildPropsPath", "_DirectoryBuildPropsFile", "_DirectoryBuildPropsBasePath",
		"CustomBeforeDirectoryBuildProps", "CustomAfterDirectoryBuildProps",
		"ImportDirectoryPackagesProps", "DirectoryPackagesPropsPath", "_DirectoryPackagesPropsFile", "_DirectoryPackagesPropsBasePath",
		"CentralPackageVersionsFileImported",
		"ImportDirectoryBuildTargets", "DirectoryBuildTargetsPath", "_DirectoryBuildTargetsFile", "_DirectoryBuildTargetsBasePath",
		"CustomBeforeDirectoryBuildTargets", "CustomAfterDirectoryBuildTargets",
		"RestorePackagesWithLockFile", "RestoreLockedMode", "NuGetLockFilePath",
		"RestoreSources", "RestoreAdditionalProjectSources",
	} {
		defaultEmpty[strings.ToLower(name)] = true
	}
}

func withTrailingSeparator(dir string) string {
	if strings.HasSuffix(dir, string(os.PathSeparator)) {
		return dir
	}
	return dir + string(os.PathSeparator)
}

// callStatic evaluates [Type]::Method(arguments) for the functions MSBuild
// projects use to locate shared props files.
func (s scope) callStatic(call string) value {
	closeType := strings.Index(call, "]")
	if closeType < 0 {
		return value{}
	}
	typeName := strings.ToLower(strings.TrimSpace(call[1:closeType]))
	rest := strings.TrimSpace(call[closeType+1:])
	if !strings.HasPrefix(rest, "::") {
		return value{}
	}
	rest = rest[2:]
	open := strings.Index(rest, "(")
	if open < 0 || matchParen(rest, open) != len(rest)-1 {
		return value{}
	}
	method := strings.ToLower(strings.TrimSpace(rest[:open]))
	var args []string
	for _, raw := range splitArguments(rest[open+1 : len(rest)-1]) {
		arg := s.expand(unquote(raw))
		if !arg.Known {
			return value{}
		}
		args = append(args, arg.Text)
	}
	projectDir := filepath.Dir(s.eval.projectPath)
	switch typeName + "::" + method {
	case "msbuild::getpathoffileabove":
		// With one argument MSBuild starts in the directory of the file that
		// contains the call (msbuild Expander.Function.cs:488-495).
		start := filepath.Dir(s.thisFile)
		switch len(args) {
		case 1:
		case 2:
			start = absoluteFrom(projectDir, args[1])
		default:
			return value{}
		}
		return value{Text: s.eval.pathOfFileAbove(args[0], start), Known: true}
	case "msbuild::getdirectorynameoffileabove":
		if len(args) != 2 {
			return value{}
		}
		path := s.eval.pathOfFileAbove(args[1], absoluteFrom(projectDir, args[0]))
		if path == "" {
			return value{Text: "", Known: true}
		}
		return value{Text: filepath.Dir(path), Known: true}
	case "msbuild::normalizepath", "system.io.path::getfullpath":
		if len(args) == 0 {
			return value{}
		}
		return value{Text: absoluteFrom(projectDir, combine(args...)), Known: true}
	case "msbuild::normalizedirectory":
		if len(args) == 0 {
			return value{}
		}
		return value{Text: withTrailingSeparator(absoluteFrom(projectDir, combine(args...))), Known: true}
	case "msbuild::ensuretrailingslash":
		if len(args) != 1 {
			return value{}
		}
		if args[0] == "" || strings.HasSuffix(args[0], "/") || strings.HasSuffix(args[0], `\`) {
			return value{Text: args[0], Known: true}
		}
		return value{Text: args[0] + string(os.PathSeparator), Known: true}
	case "msbuild::valueordefault":
		if len(args) != 2 {
			return value{}
		}
		if args[0] == "" {
			return value{Text: args[1], Known: true}
		}
		return value{Text: args[0], Known: true}
	case "system.io.path::combine":
		if len(args) == 0 {
			return value{}
		}
		return value{Text: combine(args...), Known: true}
	}
	return value{}
}

func splitArguments(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var args []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	return append(args, strings.TrimSpace(raw[start:]))
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"' || s[0] == '`') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// nativePath converts MSBuild's Windows-style separators on other systems,
// as MSBuild does for paths on Unix.
func nativePath(path string) string {
	if runtime.GOOS != "windows" {
		path = strings.ReplaceAll(path, `\`, "/")
	}
	return filepath.FromSlash(path)
}

// combine follows System.IO.Path.Combine: a rooted later part replaces the
// parts before it.
func combine(parts ...string) string {
	result := ""
	for _, part := range parts {
		part = nativePath(part)
		if part == "" {
			continue
		}
		if filepath.IsAbs(part) || result == "" {
			result = part
			continue
		}
		result = withTrailingSeparator(result) + part
	}
	return result
}

// absoluteFrom resolves a relative path against base and cleans it.
func absoluteFrom(base, path string) string {
	path = nativePath(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}

// ImportPath expands an Import element's Project value as Evaluate does, but
// sees only reserved properties such as $(MSBuildThisFileDirectory) and the
// supported property functions. It reports false when the path depends on
// any other property. Relative results resolve against the declaring file.
func ImportPath(raw, declaringFile, projectPath string) (string, bool) {
	e := &evaluator{projectPath: filepath.Clean(projectPath), props: map[string]*property{}, strict: true,
		lookup: func(string) (string, bool) { return "", false }}
	expanded := e.scope(filepath.Clean(declaringFile)).expand(raw)
	text := strings.TrimSpace(expanded.Text)
	if !expanded.Known || text == "" || strings.ContainsAny(text, "*?") {
		return "", false
	}
	return absoluteFrom(filepath.Dir(declaringFile), text), true
}
