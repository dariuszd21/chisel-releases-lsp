package parser

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Known release format versions, in increasing order.
const (
	FormatV1 = "v1"
	FormatV2 = "v2"
	FormatV3 = "v3"
	FormatV4 = "v4"
)

// KnownFormats lists every release format the LSP understands.
var KnownFormats = []string{FormatV1, FormatV2, FormatV3, FormatV4}

// KnownStoreKinds lists every store kind chisel can fetch packages from.
var KnownStoreKinds = []string{"bin"}

// Store is a `stores:` entry of chisel.yaml. Stores were introduced in format
// v3 and let a package be fetched through a store API instead of a deb archive.
type Store struct {
	Name          string
	NameRange     Range
	Kind          string
	KindRange     Range
	Version       string
	VersionRange  Range
	DefaultPrefix string
	PrefixRange   Range
}

// Release is the parsed representation of a chisel.yaml release definition.
// Only the parts the LSP needs are modelled: the format version, the archive
// names (so `archive:` values can be validated) and the store definitions.
type Release struct {
	Format       string
	FormatRange  Range
	Archives     map[string]Range // archive name → range of its key
	ArchiveOrder []string
	Stores       map[string]*Store
	StoreOrder   []string
}

// FormatAtLeast reports whether the release format is at least the given
// version. An unset or unknown format is treated as the oldest format so that
// files without a chisel.yaml never trigger version-gated diagnostics.
func (r *Release) FormatAtLeast(format string) bool {
	if r == nil {
		return false
	}
	return formatRank(r.Format) >= formatRank(format)
}

// formatRank maps a format string to a comparable rank. Unknown formats rank 0.
func formatRank(format string) int {
	for i, f := range KnownFormats {
		if f == format {
			return i + 1
		}
	}
	return 0
}

// SupportsStores reports whether the release format allows `stores:` in
// chisel.yaml and `store:`/`default-track:`/`channel:` in slice definitions.
// Stores were introduced in format v3.
func (r *Release) SupportsStores() bool {
	return r.FormatAtLeast(FormatV3)
}

// StoreSlicesDir returns the directory name, relative to the release root, that
// holds slice definitions of store-backed packages.
//
// In format v3, store packages live in a separate "bin-slices/" directory so
// that older Chisel versions — which only read "slices/" — never see the new
// store fields. From format v4 onwards they live in "slices/" alongside
// regular package definitions.
func (r *Release) StoreSlicesDir() string {
	if r != nil && r.FormatAtLeast(FormatV4) {
		return "slices"
	}
	return "bin-slices"
}

// LookupStore returns the store with the given name, or nil.
func (r *Release) LookupStore(name string) *Store {
	if r == nil {
		return nil
	}
	return r.Stores[name]
}

// ParseReleaseFile reads and parses a chisel.yaml release definition file.
func ParseReleaseFile(path string) (*Release, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return ParseReleaseBytes(data)
}

// ParseReleaseBytes parses a chisel.yaml release definition from raw bytes.
func ParseReleaseBytes(data []byte) (*Release, error) {
	rel := &Release{
		Archives: map[string]Range{},
		Stores:   map[string]*Store{},
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("yaml parse: %w", err)
	}
	if root.Kind == 0 {
		return rel, nil // empty document
	}
	doc := &root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping at root")
	}

	pairs := doc.Content
	for i := 0; i+1 < len(pairs); i += 2 {
		key, val := pairs[i], pairs[i+1]
		switch key.Value {
		case "format":
			rel.Format = val.Value
			rel.FormatRange = nodeRange(val)
		case "archives", "v2-archives":
			if val.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				name := val.Content[j]
				if _, seen := rel.Archives[name.Value]; !seen {
					rel.ArchiveOrder = append(rel.ArchiveOrder, name.Value)
				}
				rel.Archives[name.Value] = nodeRange(name)
			}
		case "stores":
			if val.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(val.Content); j += 2 {
				name, body := val.Content[j], val.Content[j+1]
				st := parseStore(name, body)
				if _, seen := rel.Stores[st.Name]; !seen {
					rel.StoreOrder = append(rel.StoreOrder, st.Name)
				}
				rel.Stores[st.Name] = st
			}
		}
	}
	return rel, nil
}

func parseStore(name, body *yaml.Node) *Store {
	st := &Store{Name: name.Value, NameRange: nodeRange(name)}
	if body.Kind != yaml.MappingNode {
		return st
	}
	for i := 0; i+1 < len(body.Content); i += 2 {
		key, val := body.Content[i], body.Content[i+1]
		switch key.Value {
		case "kind":
			st.Kind = val.Value
			st.KindRange = nodeRange(val)
		case "version":
			st.Version = val.Value
			st.VersionRange = nodeRange(val)
		case "default-prefix":
			st.DefaultPrefix = val.Value
			st.PrefixRange = nodeRange(val)
		}
	}
	return st
}
