package projectedit

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type insertionPoint struct {
	closeOffset int
	indent      string
	prefix      string
	selfClosing bool
	startEnd    int
}

// insertionPoints validates a single Project root and finds an unconditional,
// top-level ItemGroup containing the same item kind. Token offsets also work
// for compact XML, comments, namespaces, and inline closing tags.
func insertionPoints(data []byte, elementTag string) (insertionPoint, *insertionPoint, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var root insertionPoint
	var group *insertionPoint
	var candidate insertionPoint
	depth, roots := 0, 0
	eligible, matching := false, false
	for {
		before := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return root, nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				roots++
				if roots != 1 || token.Name.Local != "Project" {
					return root, nil, fmt.Errorf("expected one Project root")
				}
				root = insertionPoint{indent: lineIndent(data, before), prefix: tagPrefix(data[before:decoder.InputOffset()]), startEnd: int(decoder.InputOffset())}
				root.selfClosing = bytes.HasSuffix(bytes.TrimSpace(data[before:root.startEnd]), []byte("/>"))
			}
			if depth == 2 {
				eligible = token.Name.Local == "ItemGroup"
				for _, attr := range token.Attr {
					if attr.Name.Local == "Condition" && strings.TrimSpace(attr.Value) != "" {
						eligible = false
					}
				}
				matching = false
				candidate = insertionPoint{indent: lineIndent(data, before), prefix: tagPrefix(data[before:decoder.InputOffset()]), startEnd: int(decoder.InputOffset())}
			}
			if depth == 3 && eligible && token.Name.Local == elementTag {
				matching = true
			}
		case xml.EndElement:
			if depth == 2 && eligible && matching && group == nil {
				candidate.closeOffset = before
				copy := candidate
				group = &copy
			}
			if depth == 1 {
				root.closeOffset = before
			}
			depth--
		case xml.CharData:
			if before == 0 {
				// A leading UTF-8 BOM is an encoding marker, not root-level text.
				token = bytes.TrimPrefix(token, []byte("\xef\xbb\xbf"))
			}
			if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
				return root, nil, fmt.Errorf("text outside Project root")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return root, nil, fmt.Errorf("expected a complete Project root")
	}
	return root, group, nil
}

func lineIndent(data []byte, offset int) string {
	start := bytes.LastIndexByte(data[:offset], '\n') + 1
	prefix := data[start:offset]
	if len(bytes.TrimSpace(prefix)) == 0 {
		return string(prefix)
	}
	return ""
}

func tagPrefix(raw []byte) string {
	name := strings.FieldsFunc(strings.TrimPrefix(string(raw), "<"), func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '/' || r == '>' })[0]
	if colon := strings.IndexByte(name, ':'); colon >= 0 {
		return name[:colon+1]
	}
	return ""
}

func insertBefore(data []byte, offset int, content, closingIndent, newline string) []byte {
	start := bytes.LastIndexByte(data[:offset], '\n') + 1
	if len(bytes.TrimSpace(data[start:offset])) == 0 {
		offset = start
		content += newline
	} else {
		content = newline + content + newline + closingIndent
	}
	result := make([]byte, 0, len(data)+len(content))
	result = append(result, data[:offset]...)
	result = append(result, content...)
	return append(result, data[offset:]...)
}

func addXMLElementData(data []byte, filePath, elementTag, pkgName, version string) ([]byte, error) {
	root, group, err := insertionPoints(data, elementTag)
	if err != nil {
		return nil, fmt.Errorf("validate %s: %w", filePath, err)
	}
	elements, err := scanPackageElements(data)
	if err != nil {
		return nil, err
	}
	for _, element := range elements {
		if strings.EqualFold(element.tag, elementTag) && strings.EqualFold(element.name, pkgName) {
			return append([]byte(nil), data...), nil
		}
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	escape := func(value string) string {
		var out bytes.Buffer
		_ = xml.EscapeText(&out, []byte(value))
		return out.String()
	}
	prefix := root.prefix
	indent := root.indent + "    "
	if group != nil {
		prefix = group.prefix
		indent = group.indent + "  "
		for _, element := range elements {
			if element.tag == elementTag && element.start >= group.startEnd && element.start < group.closeOffset {
				if existing := lineIndent(data, element.start); existing != "" {
					indent = existing
					break
				}
			}
		}
	}
	item := fmt.Sprintf(`<%s%s Include="%s"`, prefix, elementTag, escape(pkgName))
	if version != "" {
		item += fmt.Sprintf(` Version="%s"`, escape(version))
	}
	item += " />"
	var after []byte
	if group != nil {
		after = insertBefore(data, group.closeOffset, indent+item, group.indent, newline)
	} else {
		groupIndent := root.indent + "  "
		block := groupIndent + "<" + prefix + "ItemGroup>" + newline + indent + item + newline + groupIndent + "</" + prefix + "ItemGroup>"
		if root.selfClosing {
			// Expand only the root's closing marker and preserve its attributes.
			after = append([]byte(nil), data[:root.startEnd-2]...)
			after = append(after, []byte(">"+newline+block+newline+root.indent+"</"+root.prefix+"Project>")...)
			after = append(after, data[root.startEnd:]...)
		} else {
			after = insertBefore(data, root.closeOffset, block, root.indent, newline)
		}
	}
	if _, _, err := insertionPoints(after, elementTag); err != nil {
		return nil, fmt.Errorf("validate resulting %s: %w", filePath, err)
	}
	return after, nil
}
