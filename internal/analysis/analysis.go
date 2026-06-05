// Package analysis provides static analysis passes over chisel slice definitions:
//   - Glob pattern validation for contents: paths
//   - Slice collision detection (same concrete path in different packages)
package analysis

import (
	"path"
	"strings"

	"github.com/canonical/chisel-releases-lsp/internal/index"
	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

// Severity mirrors LSP diagnostic severity values.
type Severity int

const (
	SeverityError   Severity = 1
	SeverityWarning Severity = 2
)

// Diagnostic is a range-based issue in a specific file.
type Diagnostic struct {
	File     string
	Range    parser.Range
	Message  string
	Severity Severity
}

// ValidateGlobs inspects every contents: path in sf and returns diagnostics for
// any path that is not a valid glob pattern.
func ValidateGlobs(filePath string, sf *parser.SliceFile) []Diagnostic {
	var diags []Diagnostic
	for _, sd := range sf.Slices {
		for _, ce := range sd.Contents {
			if msg := validateGlobPath(ce.Path); msg != "" {
				diags = append(diags, Diagnostic{
					File:     filePath,
					Range:    ce.PathRange,
					Message:  msg,
					Severity: SeverityError,
				})
			}
		}
	}
	return diags
}

// validateGlobPath returns an error message if p is not a valid glob, or "".
// Chisel uses Go's path.Match-style globs but also allows **, so we normalise
// ** → "*" for the stdlib check.
func validateGlobPath(p string) string {
	if p == "" {
		return "content path must not be empty"
	}
	if !strings.HasPrefix(p, "/") {
		return "content path must be absolute (start with /)"
	}
	// Replace ** with a single segment placeholder for validation purposes.
	normalised := strings.ReplaceAll(p, "**", "STARSTAR")
	_, err := path.Match(normalised, normalised)
	if err != nil {
		return "invalid glob pattern: " + err.Error()
	}
	// Validate that ** only appears as a complete path segment or at the end.
	for _, seg := range strings.Split(p, "/") {
		if strings.Contains(seg, "**") && seg != "**" {
			return "** must appear as a standalone path segment (e.g. /dir/**/file is not valid; use /dir/**)"
		}
	}
	return ""
}

// Collision describes two slices from different packages that conflict on a path.
type Collision struct {
	Path     string
	SliceA   string // pkg_slice
	FileA    string
	RangeA   parser.Range
	SliceB   string // pkg_slice
	FileB    string
	RangeB   parser.Range
}

// DetectCollisions finds all concrete (non-glob) paths that are claimed by
// slices in two or more different packages with incompatible definitions.
// Compatible = path appears in slices of the same package, or both entries
// have identical inline attributes (both are plain extractions with no copy/text/etc).
// For LSP purposes we flag cross-package duplicate concrete paths as collisions.
func DetectCollisions(idx *index.Index) []Collision {
	type entry struct {
		pkg      string
		sliceName string
		file     string
		r        parser.Range
	}
	// path → list of entries
	pathMap := make(map[string][]entry)

	for _, filePath := range idx.AllFiles() {
		sf := idx.FileSliceFile(filePath)
		if sf == nil {
			continue
		}
		for sliceName, sd := range sf.Slices {
			for _, ce := range sd.Contents {
				if isGlob(ce.Path) {
					continue
				}
				pathMap[ce.Path] = append(pathMap[ce.Path], entry{
					pkg:       sf.Package,
					sliceName: sliceName,
					file:      filePath,
					r:         ce.PathRange,
				})
			}
		}
	}

	var collisions []Collision
	for p, entries := range pathMap {
		// Group by package
		pkgSet := make(map[string][]entry)
		for _, e := range entries {
			pkgSet[e.pkg] = append(pkgSet[e.pkg], e)
		}
		if len(pkgSet) < 2 {
			continue // same package owning it multiple times is allowed
		}
		// Collect one representative entry per package for collision reporting
		var reps []entry
		for _, es := range pkgSet {
			reps = append(reps, es[0])
		}
		for i := 0; i < len(reps); i++ {
			for j := i + 1; j < len(reps); j++ {
				a, b := reps[i], reps[j]
				collisions = append(collisions, Collision{
					Path:   p,
					SliceA: a.pkg + "_" + a.sliceName,
					FileA:  a.file,
					RangeA: a.r,
					SliceB: b.pkg + "_" + b.sliceName,
					FileB:  b.file,
					RangeB: b.r,
				})
			}
		}
	}
	return collisions
}

// isGlob reports whether a path contains glob metacharacters.
func isGlob(p string) bool {
	return strings.ContainsAny(p, "*?[")
}
