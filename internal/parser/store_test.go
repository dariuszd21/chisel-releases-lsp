package parser_test

import (
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

const storePkg = `
package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    essential:
      bin-curl_copyright:
      libc6_libs:
        channel: 2.0/edge
      libssl3_libs:
        channel: [2.0/*, 3.0/!stable]
    contents:
      /usr/bin/curl:
      /usr/bin/curl-new: {channel: 3.0/edge}
      /usr/share/doc/curl/**: {channel: [3.0/stable, 2.0/beta]}
`

func TestParseSliceFile_StoreFields(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(storePkg))
	if err != nil {
		t.Fatal(err)
	}
	if sf.Package != "curl" {
		t.Errorf("Package: got %q, want %q", sf.Package, "curl")
	}
	if sf.Store != "bin" {
		t.Errorf("Store: got %q, want %q", sf.Store, "bin")
	}
	if sf.DefaultTrack != "3.0" {
		t.Errorf("DefaultTrack: got %q, want %q", sf.DefaultTrack, "3.0")
	}
	if sf.StoreRange.Start.Line != 2 {
		t.Errorf("StoreRange line: got %d, want 2", sf.StoreRange.Start.Line)
	}
	if sf.DefaultTrackRange.Start.Line != 3 {
		t.Errorf("DefaultTrackRange line: got %d, want 3", sf.DefaultTrackRange.Start.Line)
	}
}

func TestParseSliceFile_Archive(t *testing.T) {
	sf, err := parser.ParseBytes([]byte("package: curl\narchive: ubuntu\nslices:\n  bins:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if sf.Archive != "ubuntu" {
		t.Errorf("Archive: got %q, want %q", sf.Archive, "ubuntu")
	}
}

func TestParseSliceFile_EssentialChannels(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(storePkg))
	if err != nil {
		t.Fatal(err)
	}
	sd := sf.Slices["bins"]
	if sd == nil {
		t.Fatal("slice 'bins' not parsed")
	}
	byRef := map[string][]string{}
	for _, e := range sd.Essential {
		var vals []string
		for _, p := range e.Channel {
			vals = append(vals, p.Value)
		}
		byRef[e.Value] = vals
	}
	// An entry without a channel is unconstrained.
	if got := byRef["bin-curl_copyright"]; len(got) != 0 {
		t.Errorf("copyright channel: got %v, want none", got)
	}
	// A scalar channel yields exactly one pattern.
	if got := byRef["libc6_libs"]; len(got) != 1 || got[0] != "2.0/edge" {
		t.Errorf("libc6_libs channel: got %v, want [2.0/edge]", got)
	}
	// A sequence channel yields every pattern, in order.
	got := byRef["libssl3_libs"]
	if len(got) != 2 || got[0] != "2.0/*" || got[1] != "3.0/!stable" {
		t.Errorf("libssl3_libs channel: got %v, want [2.0/* 3.0/!stable]", got)
	}
}

func TestParseSliceFile_ContentChannels(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(storePkg))
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string][]string{}
	ranges := map[string]parser.Range{}
	for _, ce := range sf.Slices["bins"].Contents {
		var vals []string
		for _, p := range ce.Channel {
			vals = append(vals, p.Value)
		}
		byPath[ce.Path] = vals
		ranges[ce.Path] = ce.ChannelKeyRange
	}
	if got := byPath["/usr/bin/curl"]; len(got) != 0 {
		t.Errorf("/usr/bin/curl channel: got %v, want none", got)
	}
	if got := byPath["/usr/bin/curl-new"]; len(got) != 1 || got[0] != "3.0/edge" {
		t.Errorf("/usr/bin/curl-new channel: got %v, want [3.0/edge]", got)
	}
	got := byPath["/usr/share/doc/curl/**"]
	if len(got) != 2 || got[0] != "3.0/stable" || got[1] != "2.0/beta" {
		t.Errorf("glob channel: got %v, want [3.0/stable 2.0/beta]", got)
	}
	// The channel key range must be set so field-wide errors have a position.
	if ranges["/usr/bin/curl-new"] == (parser.Range{}) {
		t.Error("ChannelKeyRange must be set when channel: is present")
	}
}

// TestParseSliceFile_ChannelWithPrefer verifies that channel: and prefer: are
// both picked up from the same attribute mapping. Parsing prefer used to break
// out of the attribute loop early, which would have hidden a later channel:.
func TestParseSliceFile_ChannelWithPrefer(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(`
package: curl
store: bin
default-track: "3.0"
slices:
  bins:
    contents:
      /usr/bin/curl: {prefer: other-pkg, channel: 3.0/edge}
`))
	if err != nil {
		t.Fatal(err)
	}
	ce := sf.Slices["bins"].Contents[0]
	if ce.Prefer != "other-pkg" {
		t.Errorf("Prefer: got %q, want %q", ce.Prefer, "other-pkg")
	}
	if len(ce.Channel) != 1 || ce.Channel[0].Value != "3.0/edge" {
		t.Errorf("Channel: got %v, want [3.0/edge]", ce.Channel)
	}
}
