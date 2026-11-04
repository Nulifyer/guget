package projectedit

import (
	"fmt"
	"regexp"
	"strings"
)

var literalNuGetVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
var floatingNuGetVersion = regexp.MustCompile(`^(?:(?:\*|[0-9]+(?:\.[0-9]+){0,2}\.\*)(?:-\*)?|[0-9]+(?:\.[0-9]+){0,3}-(?:[0-9A-Za-z-]+\.)?\*)$`)

// ValidateVersion accepts literal NuGet versions, ranges, and floating versions.
// MSBuild expressions require property-owner editing and are refused by Guget.
func ValidateVersion(version string) error {
	if literalNuGetVersion.MatchString(version) || floatingNuGetVersion.MatchString(version) {
		return nil
	}
	if len(version) >= 3 && strings.ContainsRune("[(", rune(version[0])) && strings.ContainsRune(")]", rune(version[len(version)-1])) {
		inner := version[1 : len(version)-1]
		if !strings.Contains(inner, ",") {
			if version[0] == '[' && version[len(version)-1] == ']' && literalNuGetVersion.MatchString(inner) {
				return nil
			}
		} else if strings.Count(inner, ",") == 1 {
			lower, upper, _ := strings.Cut(inner, ",")
			lower, upper = strings.TrimSpace(lower), strings.TrimSpace(upper)
			if (lower != "" || upper != "") && (lower == "" || literalNuGetVersion.MatchString(lower)) && (upper == "" || literalNuGetVersion.MatchString(upper)) && (lower != "" || version[0] == '(') && (upper != "" || version[len(version)-1] == ')') {
				return nil
			}
		}
	}
	return fmt.Errorf("invalid NuGet version %q; use a literal version, version range, or floating version", version)
}
