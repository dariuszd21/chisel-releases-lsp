// chisel-releases-lsp is a Language Server Protocol server for chisel slice
// definition files. It communicates over stdio and works with any LSP-capable
// editor or IDE.
//
// Usage:
//
//	chisel-releases-lsp
//
// The server expects the editor to send the workspace root (a directory
// containing a `slices/` subdirectory) via the LSP `initialize` request's
// `rootUri` or `rootPath` field.
package main

import (
	"fmt"
	"os"

	"github.com/canonical/chisel-releases-lsp/internal/lsp"
)

func main() {
	srv := lsp.New()
	if err := srv.RunStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "chisel-releases-lsp: %v\n", err)
		os.Exit(1)
	}
}
