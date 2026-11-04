// Package projectedit plans source-preserving NuGet XML edits independently of the UI.
package projectedit

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	editplan "github.com/nulifyer/guget/internal/edit"
)

func isExactLock(s string) bool {
	return len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' && !strings.ContainsRune(s, ',')
}

type packageElement struct {
	conditional bool
	tag         string
	name        string
	start       int
	startTagEnd int
	end         int
}

// scanPackageElements uses the XML tokenizer to find exact element byte ranges.
// The editor can then preserve comments and formatting outside the target node.
func scanPackageElements(data []byte) ([]packageElement, error) {
	if _, _, err := insertionPoints(data, ""); err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var active []packageElement
	var found []packageElement
	var conditions []bool
	for {
		before := int(decoder.InputOffset())
		tok, err := decoder.Token()
		after := int(decoder.InputOffset())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse project XML: %w", err)
		}
		switch token := tok.(type) {
		case xml.StartElement:
			conditional := len(conditions) > 0 && conditions[len(conditions)-1]
			for _, attr := range token.Attr {
				if strings.EqualFold(attr.Name.Local, "Condition") && strings.TrimSpace(attr.Value) != "" {
					conditional = true
				}
			}
			conditions = append(conditions, conditional)
			if conditional && len(active) > 0 {
				active[len(active)-1].conditional = true
			}
			if !strings.EqualFold(token.Name.Local, "PackageReference") &&
				!strings.EqualFold(token.Name.Local, "PackageVersion") {
				continue
			}
			name := ""
			for _, attr := range token.Attr {
				if strings.EqualFold(attr.Name.Local, "Include") || strings.EqualFold(attr.Name.Local, "Update") {
					name = attr.Value
					break
				}
			}
			active = append(active, packageElement{
				tag: token.Name.Local, name: name, start: before, startTagEnd: after, conditional: conditional,
			})
		case xml.EndElement:
			conditions = conditions[:len(conditions)-1]
			if len(active) == 0 || !strings.EqualFold(active[len(active)-1].tag, token.Name.Local) {
				continue
			}
			element := active[len(active)-1]
			active = active[:len(active)-1]
			element.end = after
			found = append(found, element)
		}
	}
	return found, nil
}

func attrPattern(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(\b` + regexp.QuoteMeta(name) + `\s*=\s*["'])[^"']*(["'])`)
}

func childPattern(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?is)(<\s*` + regexp.QuoteMeta(name) + `\b[^>]*>)[^<]*(</\s*` + regexp.QuoteMeta(name) + `\s*>)`)
}

func replaceElementVersion(raw []byte, tag, version string) ([]byte, bool) {
	startEnd := bytes.IndexByte(raw, '>')
	if startEnd < 0 {
		return raw, false
	}
	startTag := raw[:startEnd+1]
	fields := []string{"Version"}
	if strings.EqualFold(tag, "PackageReference") {
		fields = []string{"VersionOverride", "Version"}
	}
	for _, field := range fields {
		re := attrPattern(field)
		if re.Match(startTag) {
			updated := replaceVersionMatch(re, startTag, version)
			return append(append([]byte(nil), updated...), raw[startEnd+1:]...), true
		}
		re = childPattern(field)
		if re.Match(raw) {
			return replaceVersionMatch(re, raw, version), true
		}
	}
	return raw, false
}

// replaceVersionMatch inserts version as literal text. regexp replacement
// strings interpret '$' specially, but MSBuild version expressions may
// legitimately contain property syntax such as $(VersionProperty).
func replaceVersionMatch(re *regexp.Regexp, input []byte, version string) []byte {
	match := re.FindSubmatchIndex(input)
	if len(match) < 6 {
		return input
	}
	result := make([]byte, 0, len(input)+len(version))
	result = append(result, input[:match[0]]...)
	result = append(result, input[match[2]:match[3]]...)
	result = append(result, version...)
	result = append(result, input[match[4]:match[5]]...)
	result = append(result, input[match[1]:]...)
	return result
}

func removeElementBytes(data []byte, element packageElement) []byte {
	lineStart := bytes.LastIndexByte(data[:element.start], '\n') + 1
	lineEnd := len(data)
	if rel := bytes.IndexByte(data[element.end:], '\n'); rel >= 0 {
		lineEnd = element.end + rel + 1
	}
	if len(bytes.TrimSpace(data[lineStart:element.start])) == 0 &&
		len(bytes.TrimSpace(data[element.end:lineEnd])) == 0 {
		return append(append([]byte(nil), data[:lineStart]...), data[lineEnd:]...)
	}
	return append(append([]byte(nil), data[:element.start]...), data[element.end:]...)
}

// RemovePackageReference removes the exact PackageReference element for pkgName.
// PackageVersion elements are left alone because a central version can be shared.
func RemovePackageReference(filePath, pkgName string) error {
	change, err := PlanRemovePackageReference(filePath, pkgName)
	if err != nil {
		return err
	}
	plan, err := editplan.NewPlan(change)
	if err != nil {
		return err
	}
	_, err = plan.Apply()
	return err
}

func PlanRemovePackageReference(filePath, pkgName string) (editplan.Change, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return editplan.Change{}, fmt.Errorf("read %s: %w", filePath, err)
	}

	elements, err := scanPackageElements(data)
	if err != nil {
		return editplan.Change{}, fmt.Errorf("scan %s: %w", filePath, err)
	}
	matches := 0
	for _, element := range elements {
		if strings.EqualFold(element.tag, "PackageReference") && strings.EqualFold(element.name, pkgName) {
			if element.conditional {
				return editplan.Change{}, fmt.Errorf("package %q in %s has conditional ownership and is read-only", pkgName, filePath)
			}
			matches++
		}
	}
	if matches > 1 {
		return editplan.Change{}, fmt.Errorf("package %q has %d PackageReference nodes in %s; conditional or duplicate ownership is read-only", pkgName, matches, filePath)
	}
	after := data
	for i := len(elements) - 1; i >= 0; i-- {
		element := elements[i]
		if strings.EqualFold(element.tag, "PackageReference") && strings.EqualFold(element.name, pkgName) {
			after = removeElementBytes(data, element)
			break
		}
	}
	return newProjectChange(filePath, data, after)
}

// UpdatePackageVersion rewrites the Version attribute for a specific
// PackageReference in a .csproj/.fsproj file without altering any other
// formatting.
func UpdatePackageVersion(filePath, pkgName, newVersion string) error {
	change, err := PlanUpdatePackageVersion(filePath, pkgName, newVersion)
	if err != nil {
		return err
	}
	plan, err := editplan.NewPlan(change)
	if err != nil {
		return err
	}
	_, err = plan.Apply()
	return err
}

func PlanUpdatePackageVersion(filePath, pkgName, newVersion string) (editplan.Change, error) {
	if err := ValidateVersion(newVersion); err != nil {
		return editplan.Change{}, err
	}
	if strings.Contains(newVersion, "$(") {
		return editplan.Change{}, fmt.Errorf("MSBuild property expressions are read-only edit targets: %q", newVersion)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return editplan.Change{}, fmt.Errorf("read %s: %w", filePath, err)
	}
	originalData := append([]byte(nil), data...)

	elements, err := scanPackageElements(data)
	if err != nil {
		return editplan.Change{}, fmt.Errorf("scan %s: %w", filePath, err)
	}
	matches := 0
	for _, element := range elements {
		if strings.EqualFold(element.name, pkgName) {
			if element.conditional {
				return editplan.Change{}, fmt.Errorf("package %q in %s has conditional ownership and is read-only", pkgName, filePath)
			}
			matches++
		}
	}
	if matches > 1 {
		return editplan.Change{}, fmt.Errorf("package %q has %d version-bearing nodes in %s; conditional or duplicate ownership is read-only", pkgName, matches, filePath)
	}
	changed := false
	for i := len(elements) - 1; i >= 0; i-- {
		element := elements[i]
		if !strings.EqualFold(element.name, pkgName) {
			continue
		}
		elementBytes := data[element.start:element.end]
		expression := packageElementVersionExpression(elementBytes, element.tag)
		if strings.Contains(expression, "$(") {
			return editplan.Change{}, fmt.Errorf("package %q in %s uses MSBuild version expression %q; edit its property owner explicitly", pkgName, filePath, expression)
		}
		replacement := newVersion
		if isExactLock(expression) && !isExactLock(replacement) {
			replacement = "[" + replacement + "]"
			if err := ValidateVersion(replacement); err != nil {
				return editplan.Change{}, fmt.Errorf("cannot replace an exact pin with %q", newVersion)
			}
		}
		raw, ok := replaceElementVersion(elementBytes, element.tag, replacement)
		if ok {
			data = append(append(append([]byte(nil), data[:element.start]...), raw...), data[element.end:]...)
			changed = true
		}
	}

	if !changed {
		return editplan.Change{}, fmt.Errorf("package %q in %s has no editable literal version", pkgName, filePath)
	}
	return newProjectChange(filePath, originalData, data)
}

func packageElementVersionExpression(raw []byte, tag string) string {
	var element struct {
		VersionAttr         string `xml:"Version,attr"`
		VersionOverrideAttr string `xml:"VersionOverride,attr"`
		VersionChild        string `xml:"Version"`
		OverrideChild       string `xml:"VersionOverride"`
	}
	if xml.Unmarshal(raw, &element) != nil {
		return ""
	}
	if strings.EqualFold(tag, "PackageReference") {
		for _, value := range []string{element.VersionOverrideAttr, element.OverrideChild, element.VersionAttr, element.VersionChild} {
			if value != "" {
				return value
			}
		}
		return ""
	}
	if element.VersionAttr != "" {
		return element.VersionAttr
	}
	return element.VersionChild
}

// AddPackageReference inserts a new <PackageReference> element into a project or props file.
// If version is empty, the element is written without a Version attribute (for CPM projects).
func AddPackageReference(filePath, pkgName, version string) error {
	return addXMLElement(filePath, "PackageReference", pkgName, version)
}

func PlanAddPackageReference(filePath, pkgName, version string) (editplan.Change, error) {
	return planAddXMLElement(filePath, "PackageReference", pkgName, version)
}

// AddPackageVersion inserts a new <PackageVersion> element into a Directory.Packages.props file.
func AddPackageVersion(filePath, pkgName, version string) error {
	return addXMLElement(filePath, "PackageVersion", pkgName, version)
}

func PlanAddPackageVersion(filePath, pkgName, version string) (editplan.Change, error) {
	return planAddXMLElement(filePath, "PackageVersion", pkgName, version)
}

// addXMLElement inserts a new XML element (PackageReference or PackageVersion) into a
// project or props file without altering any other formatting.
func addXMLElement(filePath, elementTag, pkgName, version string) error {
	change, err := planAddXMLElement(filePath, elementTag, pkgName, version)
	if err != nil {
		return err
	}
	plan, err := editplan.NewPlan(change)
	if err != nil {
		return err
	}
	_, err = plan.Apply()
	return err
}

func planAddXMLElement(filePath, elementTag, pkgName, version string) (editplan.Change, error) {
	if version != "" {
		if err := ValidateVersion(version); err != nil {
			return editplan.Change{}, err
		}
	}
	if strings.Contains(version, "$(") {
		return editplan.Change{}, fmt.Errorf("MSBuild property expressions are read-only edit targets: %q", version)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return editplan.Change{}, fmt.Errorf("read %s: %w", filePath, err)
	}
	after, err := addXMLElementData(data, filePath, elementTag, pkgName, version)
	if err != nil {
		return editplan.Change{}, err
	}
	return newProjectChange(filePath, data, after)
}

func newProjectChange(filePath string, before, after []byte) (editplan.Change, error) {
	if _, _, err := insertionPoints(after, ""); err != nil {
		return editplan.Change{}, fmt.Errorf("validate resulting %s: %w", filePath, err)
	}
	return editplan.NewChange(filePath, before, after)
}
