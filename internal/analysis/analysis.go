// Package analysis provides static analysis passes over chisel slice definitions:
//   - Glob pattern validation for contents: paths
//   - Slice collision detection (same concrete path in different packages)
//   - Package name ↔ filename consistency
package analysis

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

// Severity mirrors LSP diagnostic severity values.
type Severity int

// Severity constants mirror LSP diagnostic severity values.
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
	for _, name := range sf.SliceOrder {
		sd := sf.Slices[name]
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

// CheckPackageName returns a diagnostic if the file's package: value does not
// match the filename stem (e.g. "openssl.yaml" must declare package: openssl).
// Returns nil when the file has no package name or the names match.
func CheckPackageName(filePath string, sf *parser.SliceFile) *Diagnostic {
	if sf.Package == "" {
		return nil
	}
	stem := strings.TrimSuffix(filepath.Base(filePath), ".yaml")
	if sf.Package == stem {
		return nil
	}
	return &Diagnostic{
		File:  filePath,
		Range: sf.PackageRange,
		Message: fmt.Sprintf(
			"package name %q does not match filename %q (expected %q)",
			sf.Package, filepath.Base(filePath), stem,
		),
		Severity: SeverityWarning,
	}
}

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
	// Validate that ** only appears as a complete path segment (e.g. /dir/**/file is fine,
	// but /dir/foo** or /dir/**.so are not).
	for _, seg := range strings.Split(p, "/") {
		if strings.Contains(seg, "**") && seg != "**" {
			return "** must appear as a standalone path segment (e.g. /dir/**foo is not valid)"
		}
	}
	return ""
}

// Collision describes two slices from different packages that conflict on a path.
type Collision struct {
	Path   string
	SliceA string // pkg_slice
	FileA  string
	RangeA parser.Range
	SliceB string // pkg_slice
	FileB  string
	RangeB parser.Range
}

// DetectCollisions finds all concrete (non-glob) paths that are claimed by
// slices in two or more different packages with incompatible definitions.
// Compatible = path appears in slices of the same package, or both entries
// have identical inline attributes (both are plain extractions with no copy/text/etc).
// For LSP purposes we flag cross-package duplicate concrete paths as collisions.
func DetectCollisions(idx *index.Index) []Collision {
	type entry struct {
		pkg       string
		sliceName string
		file      string
		r         parser.Range
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
	// Sort paths for deterministic output.
	paths := make([]string, 0, len(pathMap))
	for p := range pathMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		entries := pathMap[p]
		// Group by package
		pkgSet := make(map[string][]entry)
		for _, e := range entries {
			pkgSet[e.pkg] = append(pkgSet[e.pkg], e)
		}
		if len(pkgSet) < 2 {
			continue // same package owning it multiple times is allowed
		}
		// Collect one representative entry per package; sort by pkg for determinism.
		var reps []entry
		for _, es := range pkgSet {
			reps = append(reps, es[0])
		}
		sort.Slice(reps, func(i, j int) bool { return reps[i].pkg < reps[j].pkg })
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

// CheckCopyrightEssential returns a Warning diagnostic for every slice that does
// not reference <pkg>_copyright in its effective essentials (package-level or
// slice-level). The copyright slice itself is exempt.
func CheckCopyrightEssential(filePath string, sf *parser.SliceFile) []Diagnostic {
	if sf.Package == "" {
		return nil
	}
	target := sf.Package + "_copyright"

	// If the package-level essential already includes the copyright ref,
	// all slices inherit it — nothing to warn about.
	for _, ref := range sf.Essential {
		if ref.Value == target {
			return nil
		}
	}

	var diags []Diagnostic
	for _, sliceName := range sf.SliceOrder {
		if sliceName == "copyright" {
			continue
		}
		sd := sf.Slices[sliceName]
		found := false
		for _, ref := range sd.Essential {
			if ref.Value == target {
				found = true
				break
			}
		}
		if !found {
			diags = append(diags, Diagnostic{
				File:  filePath,
				Range: sd.NameRange,
				Message: fmt.Sprintf(
					"slice %q is missing essential %q",
					sliceName, target,
				),
				Severity: SeverityWarning,
			})
		}
	}
	return diags
}

// DuplicateSlice describes two files that both define the same package+slice name.
// The second definition silently overwrites the first in the index, so the user
// may see inconsistent hover/reference results without realising why.
type DuplicateSlice struct {
	Pkg       string
	SliceName string
	File1     string       // file that defined the slice first (alphabetically)
	Range1    parser.Range // NameRange of the slice key in File1
	File2     string       // second definition
	Range2    parser.Range
}

// DetectDuplicateSlices finds all (package, sliceName) pairs that are defined
// in more than one file. Results are sorted by Pkg then SliceName for determinism.
func DetectDuplicateSlices(idx *index.Index) []DuplicateSlice {
	type entry struct {
		file string
		rng  parser.Range
	}
	// (pkg+":"+sliceName) → first entry seen
	seen := make(map[string]entry)

	var dups []DuplicateSlice
	for _, filePath := range idx.AllFiles() {
		sf := idx.FileSliceFile(filePath)
		if sf == nil || sf.Package == "" {
			continue
		}
		for _, sliceName := range sf.SliceOrder {
			sd := sf.Slices[sliceName]
			key := sf.Package + ":" + sliceName
			if prev, exists := seen[key]; exists {
				dups = append(dups, DuplicateSlice{
					Pkg:       sf.Package,
					SliceName: sliceName,
					File1:     prev.file,
					Range1:    prev.rng,
					File2:     filePath,
					Range2:    sd.NameRange,
				})
			} else {
				seen[key] = entry{filePath, sd.NameRange}
			}
		}
	}
	sort.Slice(dups, func(i, j int) bool {
		if dups[i].Pkg != dups[j].Pkg {
			return dups[i].Pkg < dups[j].Pkg
		}
		return dups[i].SliceName < dups[j].SliceName
	})
	return dups
}

// DuplicateEssential describes a repeated pkg_slice reference. This covers two
// cases:
//   - Within a single block: the same value appears more than once in the same
//     package-level or per-slice essential: list.
//   - Cross-block redundancy: a slice-level essential repeats a value already
//     present in the package-level essential (which all slices inherit).
//
// SliceName is empty when the duplicate is in the package-level block.
// When SliceName is non-empty and FirstRef.ValueRange is the zero value,
// the first occurrence is in the package-level block.
type DuplicateEssential struct {
	File      string
	SliceName string // empty for package-level essential
	FirstRef  parser.EssentialRef
	DupRef    parser.EssentialRef
}

// CheckDuplicateEssentials returns a Warning for every essential reference that
// is redundant:
//   - Appearing more than once in the same essential: block (package-level or
//     per-slice).
//   - Appearing in a slice-level essential: block when the same value is already
//     present in the package-level essential: (all slices inherit it).
func CheckDuplicateEssentials(filePath string, sf *parser.SliceFile) []DuplicateEssential {
	var result []DuplicateEssential

	// Build a map of package-level refs for fast lookup.
	pkgLevel := make(map[string]parser.EssentialRef, len(sf.Essential))

	// Check within-block duplicates for the package-level list.
	seenPkg := make(map[string]parser.EssentialRef)
	for _, ref := range sf.Essential {
		if first, exists := seenPkg[ref.Value]; exists {
			result = append(result, DuplicateEssential{
				File:     filePath,
				FirstRef: first,
				DupRef:   ref,
			})
		} else {
			seenPkg[ref.Value] = ref
			pkgLevel[ref.Value] = ref
		}
	}

	// Check each slice's essential block.
	for _, sliceName := range sf.SliceOrder {
		seen := make(map[string]parser.EssentialRef)
		for _, ref := range sf.Slices[sliceName].Essential {
			// Cross-block: already covered by package-level essential.
			if firstPkg, coveredByPkg := pkgLevel[ref.Value]; coveredByPkg {
				result = append(result, DuplicateEssential{
					File:      filePath,
					SliceName: sliceName,
					FirstRef:  firstPkg,
					DupRef:    ref,
				})
				continue
			}
			// Within-block: already seen in this slice's list.
			if first, exists := seen[ref.Value]; exists {
				result = append(result, DuplicateEssential{
					File:      filePath,
					SliceName: sliceName,
					FirstRef:  first,
					DupRef:    ref,
				})
			} else {
				seen[ref.Value] = ref
			}
		}
	}
	return result
}
