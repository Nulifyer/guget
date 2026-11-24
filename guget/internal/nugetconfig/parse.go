package nugetconfig

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// configFile holds the sections of one NuGet.Config that affect sources.
type configFile struct {
	path        string
	sources     section[sourceItem]
	disabled    section[string]
	credentials []credentialItem
	mapping     section[mappingItem]
}

// section keeps the items after the last <clear/>; cleared records that the
// section discards everything inherited from lower-priority files.
type section[T any] struct {
	items   []T
	cleared bool
}

func (s *section[T]) clear() {
	s.items = nil
	s.cleared = true
}

type sourceItem struct {
	key, value string
}

type credentialItem struct {
	source    string // decoded element name
	username  string
	password  string // DPAPI-encrypted
	clearText string
	encrypted bool
	authTypes string
}

type mappingItem struct {
	key      string
	patterns []string
}

type xmlElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr   `xml:",any,attr"`
	Children []xmlElement `xml:",any"`
}

func (e xmlElement) attr(name string) (string, bool) {
	for _, attr := range e.Attrs {
		if strings.EqualFold(attr.Name.Local, name) {
			return attr.Value, true
		}
	}
	return "", false
}

func (e xmlElement) is(name string) bool {
	return strings.EqualFold(e.XMLName.Local, name)
}

// parseConfigFile parses the source-related sections of one file. Errors never
// quote values because configuration files can hold credentials.
func parseConfigFile(path string, data []byte) (*configFile, error) {
	var root xmlElement
	if err := decodeSingleRoot(data, &root); err != nil {
		return nil, err
	}
	if root.XMLName.Local != "configuration" {
		return nil, fmt.Errorf("root element must be <configuration>")
	}
	file := &configFile{path: path}
	for _, sectionElement := range root.Children {
		var err error
		switch sectionElement.XMLName.Local {
		case "packageSources":
			err = parseSources(sectionElement, &file.sources)
		case "disabledPackageSources":
			err = parseDisabled(sectionElement, &file.disabled)
		case "packageSourceCredentials":
			err = parseCredentials(sectionElement, file)
		case "packageSourceMapping":
			err = parseMapping(sectionElement, &file.mapping)
		}
		if err != nil {
			return nil, fmt.Errorf("<%s>: %w", sectionElement.XMLName.Local, err)
		}
	}
	return file, nil
}

func addKeyValue(element xmlElement) (string, string, error) {
	key, _ := element.attr("key")
	value, hasValue := element.attr("value")
	if strings.TrimSpace(key) == "" {
		return "", "", fmt.Errorf("<add> requires a non-empty key")
	}
	if !hasValue {
		return "", "", fmt.Errorf("<add key=%q> requires a value attribute", key)
	}
	return key, value, nil
}

func parseSources(element xmlElement, target *section[sourceItem]) error {
	for _, child := range element.Children {
		switch {
		case child.is("clear"):
			target.clear()
		case child.is("add"):
			key, value, err := addKeyValue(child)
			if err != nil {
				return err
			}
			target.items = append(target.items, sourceItem{key: key, value: value})
		}
	}
	return nil
}

func parseDisabled(element xmlElement, target *section[string]) error {
	for _, child := range element.Children {
		switch {
		case child.is("clear"):
			target.clear()
		case child.is("add"):
			// NuGet disables a listed source whatever its value is.
			key, _, err := addKeyValue(child)
			if err != nil {
				return err
			}
			target.items = append(target.items, key)
		}
	}
	return nil
}

// parseCredentials applies NuGet's validation: every child element is a source
// credential, so <clear/> is rejected like any credential without a username.
func parseCredentials(element xmlElement, file *configFile) error {
	for _, child := range element.Children {
		item := credentialItem{source: decodeXMLName(child.XMLName.Local)}
		var hasUser, hasPassword, hasClearText bool
		for _, entry := range child.Children {
			if !entry.is("add") {
				continue
			}
			key, value, err := addKeyValue(entry)
			if err != nil {
				return fmt.Errorf("credentials for %q: %w", item.source, err)
			}
			switch strings.ToLower(key) {
			case "username":
				if hasUser {
					return fmt.Errorf("credentials for %q have more than one Username", item.source)
				}
				hasUser, item.username = true, value
			case "password":
				hasPassword, item.password, item.encrypted = true, value, true
			case "cleartextpassword":
				hasClearText, item.clearText = true, value
			case "validauthenticationtypes":
				item.authTypes = value
			}
		}
		switch {
		case hasPassword && hasClearText:
			return fmt.Errorf("credentials for %q set both Password and ClearTextPassword", item.source)
		case !hasUser || !(hasPassword || hasClearText):
			return fmt.Errorf("credentials for %q must have a Username and a password", item.source)
		}
		file.credentials = append(file.credentials, item)
	}
	return nil
}

func parseMapping(element xmlElement, target *section[mappingItem]) error {
	keys := make(map[string]bool)
	for _, child := range element.Children {
		switch {
		case child.is("clear"):
			target.clear()
		case child.is("packageSource"):
			key, _ := child.attr("key")
			key = strings.TrimSpace(key)
			if key == "" {
				return fmt.Errorf("<packageSource> requires a non-empty key")
			}
			if keys[strings.ToLower(key)] {
				return fmt.Errorf("package source %q is mapped more than once", key)
			}
			keys[strings.ToLower(key)] = true
			item := mappingItem{key: key}
			for _, pattern := range child.Children {
				if !pattern.is("package") {
					continue
				}
				value, _ := pattern.attr("pattern")
				if value = strings.TrimSpace(value); value == "" {
					return fmt.Errorf("package source %q has an empty pattern", key)
				}
				item.patterns = append(item.patterns, value)
			}
			if len(item.patterns) == 0 {
				return fmt.Errorf("package source %q has no package patterns", key)
			}
			target.items = append(target.items, item)
		}
	}
	return nil
}

// decodeXMLName reverses XmlConvert.EncodeName escapes such as _x0020_.
func decodeXMLName(name string) string {
	var b strings.Builder
	for i := 0; i < len(name); {
		if decoded, width := decodeXMLEscape(name[i:]); width > 0 {
			b.WriteRune(decoded)
			i += width
			continue
		}
		b.WriteByte(name[i])
		i++
	}
	return b.String()
}

func decodeXMLEscape(s string) (rune, int) {
	for _, digits := range []int{4, 8} {
		width := digits + 3
		if len(s) < width || s[0] != '_' || s[1] != 'x' || s[width-1] != '_' {
			continue
		}
		if value, err := strconv.ParseUint(s[2:2+digits], 16, 32); err == nil {
			return rune(value), width
		}
	}
	return 0, 0
}

// decodeSingleRoot rejects trailing roots or text as well as malformed XML.
func decodeSingleRoot(data []byte, target any) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return safeXMLError(err)
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return safeXMLError(err)
		}
		switch token := token.(type) {
		case xml.Comment, xml.ProcInst:
		case xml.CharData:
			if len(bytes.TrimSpace(token)) != 0 {
				return fmt.Errorf("unexpected content outside the configuration root")
			}
		default:
			return fmt.Errorf("expected a single configuration root")
		}
	}
}

func safeXMLError(err error) error {
	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("malformed XML at line %d", syntax.Line)
	}
	return fmt.Errorf("invalid configuration XML")
}

// resolveLocalPath resolves a non-HTTP source against the directory of the
// configuration file that declared it.
func resolveLocalPath(value, baseDir string) string {
	if strings.Contains(value, "://") {
		return value
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, `\\`) {
		return filepath.Clean(value)
	}
	return filepath.Join(baseDir, value)
}
