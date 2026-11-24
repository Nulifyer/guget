package msbuildstatic

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// node is one MSBuild XML element. MSBuild element names are case-sensitive;
// attribute and metadata names are not.
type node struct {
	name     string
	attrs    []xml.Attr
	children []*node
	text     string
}

func (n *node) attr(name string) (string, bool) {
	for _, attr := range n.attrs {
		if attr.Name.Space == "" && strings.EqualFold(attr.Name.Local, name) {
			return attr.Value, true
		}
	}
	return "", false
}

func (n *node) attrValue(name string) string {
	value, _ := n.attr(name)
	return value
}

func parseProjectXML(data []byte) (*node, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stack []*node
	var root *node
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse XML: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			element := &node{name: token.Name.Local, attrs: append([]xml.Attr(nil), token.Attr...)}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("parse XML: multiple root elements")
				}
				root = element
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, element)
			}
			stack = append(stack, element)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(token)
			}
		}
	}
	if root == nil || root.name != "Project" {
		return nil, fmt.Errorf("parse XML: expected a Project root element")
	}
	return root, nil
}

// propertyText returns an element's text. MSBuild keeps surrounding
// whitespace in property values.
func (n *node) propertyText() string {
	return n.text
}
