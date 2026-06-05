// Package parser provides a position-aware parser for chisel slice definition files.
// It uses gopkg.in/yaml.v3 Node API to preserve line/column positions so that
// the LSP server can map editor positions back to slice names, paths, etc.
package parser

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Position holds a 0-based line and character offset within a file.
type Position struct {
	Line      int
	Character int
}

// Range is a start/end span within a file.
type Range struct {
	Start Position
	End   Position
}

// ContentEntry represents one path key under a slice's `contents:` block.
type ContentEntry struct {
	Path      string
	PathRange Range
}

// SliceDef represents a single named slice inside a package.
type SliceDef struct {
	Name       string
	NameRange  Range
	Essential  []EssentialRef
	Contents   []ContentEntry
}

// EssentialRef is a reference to another slice (pkg_slice) inside an essential list.
type EssentialRef struct {
	Value      string
	ValueRange Range
}

// SliceFile is the fully parsed representation of a slice definitions file.
type SliceFile struct {
	Package      string
	PackageRange Range
	// Top-level essential (applied to all slices in this package)
	Essential []EssentialRef
	Slices    map[string]*SliceDef
	// Ordered slice names for stable iteration
	SliceOrder []string
}

// ParseFile reads and parses a chisel slice definitions YAML file.
func ParseFile(path string) (*SliceFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseBytes(data)
}

// ParseBytes parses chisel slice YAML from raw bytes.
func ParseBytes(data []byte) (*SliceFile, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("yaml parse: %w", err)
	}
	if root.Kind == 0 {
		// Empty document
		return &SliceFile{Slices: map[string]*SliceDef{}}, nil
	}
	// Unwrap document node
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping at root")
	}
	return parseSliceFile(doc)
}

// nodeRange returns a Range for a yaml.Node using its line/column.
// yaml.v3 uses 1-based lines and 1-based columns; we convert to 0-based.
func nodeRange(n *yaml.Node) Range {
	line := n.Line - 1
	col := n.Column - 1
	end := col + len(n.Value)
	return Range{
		Start: Position{Line: line, Character: col},
		End:   Position{Line: line, Character: end},
	}
}

func parseSliceFile(mapping *yaml.Node) (*SliceFile, error) {
	sf := &SliceFile{Slices: map[string]*SliceDef{}}
	pairs := mapping.Content
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i]
		val := pairs[i+1]
		switch key.Value {
		case "package":
			sf.Package = val.Value
			sf.PackageRange = nodeRange(val)
		case "essential":
			refs, err := parseEssentialList(val)
			if err != nil {
				return nil, err
			}
			sf.Essential = refs
		case "slices":
			if val.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("slices must be a mapping")
			}
			slicePairs := val.Content
			for j := 0; j+1 < len(slicePairs); j += 2 {
				sliceName := slicePairs[j]
				sliceBody := slicePairs[j+1]
				sd, err := parseSliceDef(sliceName.Value, nodeRange(sliceName), sliceBody)
				if err != nil {
					return nil, err
				}
				sf.Slices[sliceName.Value] = sd
				sf.SliceOrder = append(sf.SliceOrder, sliceName.Value)
			}
		}
	}
	return sf, nil
}

func parseSliceDef(name string, nameRange Range, body *yaml.Node) (*SliceDef, error) {
	sd := &SliceDef{Name: name, NameRange: nameRange}
	if body.Kind != yaml.MappingNode {
		return sd, nil
	}
	pairs := body.Content
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i]
		val := pairs[i+1]
		switch key.Value {
		case "essential":
			refs, err := parseEssentialList(val)
			if err != nil {
				return nil, err
			}
			sd.Essential = refs
		case "contents":
			entries, err := parseContents(val)
			if err != nil {
				return nil, err
			}
			sd.Contents = entries
		}
	}
	return sd, nil
}

func parseEssentialList(node *yaml.Node) ([]EssentialRef, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var refs []EssentialRef
	for _, item := range node.Content {
		refs = append(refs, EssentialRef{
			Value:      item.Value,
			ValueRange: nodeRange(item),
		})
	}
	return refs, nil
}

func parseContents(node *yaml.Node) ([]ContentEntry, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}
	var entries []ContentEntry
	pairs := node.Content
	for i := 0; i < len(pairs); i += 2 {
		key := pairs[i]
		entries = append(entries, ContentEntry{
			Path:      key.Value,
			PathRange: nodeRange(key),
		})
	}
	return entries, nil
}

// SliceRefFromToken parses a string like "pkg_slice" into (pkg, slice).
// Returns empty strings if the token is not a valid slice reference.
func SliceRefFromToken(token string) (pkg, slice string) {
	idx := strings.LastIndex(token, "_")
	if idx <= 0 || idx == len(token)-1 {
		return "", ""
	}
	return token[:idx], token[idx+1:]
}
