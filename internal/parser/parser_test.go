package parser_test

import (
	"testing"

	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

const sampleYAML = `
package: openssl

essential:
  - openssl_copyright

slices:
  bins:
    essential:
      - libc6_libs
      - libssl3t64_libs
    contents:
      /usr/bin/openssl:
      /usr/bin/c_rehash:

  config:
    contents:
      /etc/ssl/openssl.cnf:
      /etc/ssl/certs/:
`

func TestParseBytes(t *testing.T) {
	sf, err := parser.ParseBytes([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	if sf.Package != "openssl" {
		t.Errorf("Package: got %q, want %q", sf.Package, "openssl")
	}

	if len(sf.Essential) != 1 || sf.Essential[0].Value != "openssl_copyright" {
		t.Errorf("top-level essential: %v", sf.Essential)
	}

	if len(sf.Slices) != 2 {
		t.Errorf("slice count: got %d, want 2", len(sf.Slices))
	}

	bins := sf.Slices["bins"]
	if bins == nil {
		t.Fatal("bins slice not found")
	}
	if len(bins.Essential) != 2 {
		t.Errorf("bins essential count: got %d, want 2", len(bins.Essential))
	}
	if len(bins.Contents) != 2 {
		t.Errorf("bins contents count: got %d, want 2", len(bins.Contents))
	}

	// Check that positions are populated (yaml.v3 is 1-based → we store 0-based)
	if bins.NameRange.Start.Line == 0 && bins.NameRange.Start.Character == 0 {
		t.Errorf("bins name range not set: %+v", bins.NameRange)
	}
}

func TestSliceRefFromToken(t *testing.T) {
	cases := []struct{ token, pkg, slice string }{
		{"libc6_libs", "libc6", "libs"},
		{"openssl_copyright", "openssl", "copyright"},
		{"linux-libc-dev_headers", "linux-libc-dev", "headers"},
		{"nounderscore", "", ""},
		{"_leading", "", ""},
		{"trailing_", "", ""},
	}
	for _, c := range cases {
		pkg, slice := parser.SliceRefFromToken(c.token)
		if pkg != c.pkg || slice != c.slice {
			t.Errorf("SliceRefFromToken(%q): got (%q,%q), want (%q,%q)", c.token, pkg, slice, c.pkg, c.slice)
		}
	}
}
