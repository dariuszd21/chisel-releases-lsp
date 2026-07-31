package analysis

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

// CheckRelease validates the `format:` and `stores:` sections of a chisel.yaml
// release definition:
//   - the format must be one of the known versions
//   - `stores:` requires format v3 or later
//   - every store must define kind, version and default-prefix
//   - the store kind must be one chisel knows how to fetch from
func CheckRelease(filePath string, rel *parser.Release) []Diagnostic {
	if rel == nil {
		return nil
	}
	var diags []Diagnostic
	add := func(r parser.Range, sev Severity, format string, args ...any) {
		diags = append(diags, Diagnostic{
			File:     filePath,
			Range:    r,
			Message:  fmt.Sprintf(format, args...),
			Severity: sev,
		})
	}

	knownFormat := slices.Contains(parser.KnownFormats, rel.Format)
	if rel.Format != "" && !knownFormat {
		add(rel.FormatRange, SeverityError, "unknown format %q, must be one of %s",
			rel.Format, strings.Join(parser.KnownFormats, ", "))
	}

	for _, name := range rel.StoreOrder {
		st := rel.Stores[name]
		// Only gate on the format when it is a known one; an unrecognised
		// format is already reported and would produce a misleading second
		// error here.
		if knownFormat && !rel.SupportsStores() {
			add(st.NameRange, SeverityError,
				"'stores' is unsupported before format %s", parser.FormatV3)
			continue
		}
		switch {
		case st.Kind == "":
			add(st.NameRange, SeverityError, "store %q is missing the 'kind' field", name)
		case !slices.Contains(parser.KnownStoreKinds, st.Kind):
			add(st.KindRange, SeverityError, "store %q has unknown kind %q, must be one of %s",
				name, st.Kind, strings.Join(parser.KnownStoreKinds, ", "))
		}
		if st.Version == "" {
			add(st.NameRange, SeverityError, "store %q is missing the 'version' field", name)
		}
		if st.DefaultPrefix == "" {
			add(st.NameRange, SeverityError, "store %q is missing the 'default-prefix' field", name)
		}
	}
	return diags
}

// CheckStoreFields validates the package-level `store:` and `default-track:`
// fields of a slice definition file against the release:
//   - both fields require format v3 or later
//   - `store:` must name a store defined in chisel.yaml
//   - `store:` and `archive:` are mutually exclusive
//   - `store:` requires `default-track:`, which must be a bare track
//   - `default-track:` requires `store:`
//   - store-backed definitions must live in the directory the format mandates
func CheckStoreFields(filePath string, sf *parser.SliceFile, rel *parser.Release) []Diagnostic {
	if sf.Store == "" && sf.DefaultTrack == "" {
		return nil
	}
	var diags []Diagnostic
	add := func(r parser.Range, sev Severity, format string, args ...any) {
		diags = append(diags, Diagnostic{
			File:     filePath,
			Range:    r,
			Message:  fmt.Sprintf(format, args...),
			Severity: sev,
		})
	}

	if rel != nil && !rel.SupportsStores() {
		r, field := sf.StoreRange, "store"
		if sf.Store == "" {
			r, field = sf.DefaultTrackRange, "default-track"
		}
		add(r, SeverityError, "'%s' is unsupported before format %s", field, parser.FormatV3)
		return diags
	}

	if sf.Store == "" {
		add(sf.DefaultTrackRange, SeverityError, "'default-track' requires 'store'")
		return diags
	}

	if rel != nil && len(rel.Stores) > 0 && rel.LookupStore(sf.Store) == nil {
		add(sf.StoreRange, SeverityError,
			"store %q is not defined in chisel.yaml", sf.Store)
	}
	if sf.Archive != "" {
		add(sf.StoreRange, SeverityError, "'store' and 'archive' are mutually exclusive")
	}
	switch {
	case sf.DefaultTrack == "":
		add(sf.StoreRange, SeverityError, "'store' requires 'default-track'")
	case strings.Contains(sf.DefaultTrack, "/"):
		add(sf.DefaultTrackRange, SeverityError,
			"'default-track' must be a bare track and must not contain '/'")
	}

	// Placement check: in format v3 store-backed definitions must be kept in
	// bin-slices/ so that Chisel versions without store support never read them.
	if rel != nil {
		if want := rel.StoreSlicesDir(); filepath.Base(filepath.Dir(filePath)) != want {
			add(sf.StoreRange, SeverityWarning,
				"store-backed package definitions must live in %s/ for format %s",
				want, rel.Format)
		}
	}
	return diags
}

// CheckChannels validates every `channel:` field of a slice definition file,
// both on `contents:` entries and on v3 map-style `essential:` entries:
//   - `channel:` requires format v3 or later
//   - `channel:` is only meaningful for store-backed packages
//   - each pattern must be a well-formed <track>/<risk>
//   - a track may appear at most once per field, so the channel set is unambiguous
func CheckChannels(filePath string, sf *parser.SliceFile, rel *parser.Release) []Diagnostic {
	var diags []Diagnostic
	add := func(r parser.Range, sev Severity, format string, args ...any) {
		diags = append(diags, Diagnostic{
			File:     filePath,
			Range:    r,
			Message:  fmt.Sprintf(format, args...),
			Severity: sev,
		})
	}

	// checkField validates one `channel:` field. keyRange points at the
	// `channel:` key and is used for whole-field errors; what describes the
	// entry that carries the field, for the message.
	checkField := func(patterns []parser.ChannelPattern, keyRange parser.Range, what string) {
		if len(patterns) == 0 {
			return
		}
		if rel != nil && !rel.SupportsStores() {
			add(keyRange, SeverityError,
				"'channel' is unsupported before format %s", parser.FormatV3)
			return
		}
		if sf.Store == "" {
			add(keyRange, SeverityError,
				"'channel' on %s requires the package to be in a store", what)
			return
		}
		seen := make(map[string]bool, len(patterns))
		for _, p := range patterns {
			track, err := ValidateChannelPattern(p.Value)
			if err != nil {
				add(p.Range, SeverityError, "invalid channel %q: %s", p.Value, err)
				continue
			}
			if seen[track] {
				add(p.Range, SeverityError, "track %q is repeated in this 'channel'", track)
				continue
			}
			seen[track] = true
		}
	}

	for _, ref := range sf.Essential {
		checkField(ref.Channel, ref.ChannelKeyRange, fmt.Sprintf("essential %q", ref.Value))
	}
	for _, name := range sf.SliceOrder {
		sd := sf.Slices[name]
		for _, ref := range sd.Essential {
			checkField(ref.Channel, ref.ChannelKeyRange, fmt.Sprintf("essential %q", ref.Value))
		}
		for _, ce := range sd.Contents {
			checkField(ce.Channel, ce.ChannelKeyRange, fmt.Sprintf("path %q", ce.Path))
		}
	}
	return diags
}

// Note on path conflicts: chisel computes conflicting paths irrespective of
// `channel:`, exactly as it does for `arch:`. Using either to partition the set
// of paths would allow combinations that are overly complex and brittle, so
// chisel is deliberately stricter. Channel patterns are only applied later, when
// extracting content for a concrete channel. Collision and redundancy checks
// therefore ignore `channel:` too.
