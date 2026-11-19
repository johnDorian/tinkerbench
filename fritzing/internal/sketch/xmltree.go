// Package sketch reads and edits Fritzing sketch files (.fz): it lists what is in a sketch and its
// connections, and adds, moves, removes and wires parts, the same way Fritzing writes them.
package sketch

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Node is an XML element. The .fz format has no mixed content, so an element has either children
// or text, and attribute order is kept so a round trip keeps the file diffable.
type Node struct {
	Name     string
	Attrs    []Attr
	Children []*Node
	Text     string
}

type Attr struct{ Name, Value string }

func (n *Node) Attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name == name {
			return a.Value
		}
	}
	return ""
}

func (n *Node) HasAttr(name string) bool {
	for _, a := range n.Attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

func (n *Node) SetAttr(name, value string) {
	for i := range n.Attrs {
		if n.Attrs[i].Name == name {
			n.Attrs[i].Value = value
			return
		}
	}
	n.Attrs = append(n.Attrs, Attr{name, value})
}

// Child returns the first child element called name, or nil.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Kids returns the child elements called name.
func (n *Node) Kids(name string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// Ensure returns the child called name, creating it (appended) when missing.
func (n *Node) Ensure(name string) *Node {
	if c := n.Child(name); c != nil {
		return c
	}
	c := &Node{Name: name}
	n.Children = append(n.Children, c)
	return c
}

// Remove drops a direct child.
func (n *Node) Remove(c *Node) {
	for i, k := range n.Children {
		if k == c {
			n.Children = append(n.Children[:i], n.Children[i+1:]...)
			return
		}
	}
}

// El builds an element from name/value attribute pairs.
func El(name string, attrs ...string) *Node {
	n := &Node{Name: name}
	for i := 0; i+1 < len(attrs); i += 2 {
		n.Attrs = append(n.Attrs, Attr{attrs[i], attrs[i+1]})
	}
	return n
}

// Add appends a child and returns it.
func (n *Node) Add(c *Node) *Node {
	n.Children = append(n.Children, c)
	return c
}

// parseXML builds the tree of the document's root element.
func parseXML(src []byte) (*Node, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Strict = true
	var stack []*Node
	var root *Node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name.Local}
			for _, a := range t.Attr {
				n.Attrs = append(n.Attrs, Attr{a.Name.Local, a.Value})
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("invalid XML: more than one root element")
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.Children = append(p.Children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				if s := string(t); strings.TrimSpace(s) != "" || len(stack[len(stack)-1].Children) == 0 {
					stack[len(stack)-1].Text += s
				}
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("invalid XML: empty document")
	}
	return root, nil
}

// serialize writes the tree indented with 4 spaces, like Fritzing does.
func serialize(root *Node) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	write(&b, root, 0)
	return b.String()
}

func write(b *strings.Builder, n *Node, depth int) {
	ind := strings.Repeat("    ", depth)
	b.WriteString(ind + "<" + n.Name)
	for _, a := range n.Attrs {
		b.WriteString(" " + a.Name + `="` + escAttr(a.Value) + `"`)
	}
	switch {
	case len(n.Children) == 0 && n.Text == "":
		b.WriteString("/>\n")
	case len(n.Children) == 0:
		b.WriteString(">" + escText(n.Text) + "</" + n.Name + ">\n")
	default:
		b.WriteString(">\n")
		for _, c := range n.Children {
			write(b, c, depth+1)
		}
		b.WriteString(ind + "</" + n.Name + ">\n")
	}
}

func escAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\r", "&#13;", "\t", "&#9;").Replace(s)
}

func escText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// num formats a coordinate the way Fritzing does (no trailing zeros).
func num(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }

func parseNum(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
