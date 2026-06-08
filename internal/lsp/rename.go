package lsp

import (
	"fmt"
	"os"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

// textDocumentPrepareRename validates that the cursor is on a renameable token
// and returns the range to pre-fill in the editor's rename dialog.
func (s *Server) textDocumentPrepareRename(_ *glsp.Context, params *protocol.PrepareRenameParams) (any, error) {
	if s.idx == nil {
		return nil, nil
	}

	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil, nil
	}

	text, ok := s.getDoc(filePath)
	if !ok {
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil, nil
		}
		text = string(data)
	}

	line := int(params.Position.Line)
	char := int(params.Position.Character)

	token := wordAtPosition(text, line, char)
	if token == "" {
		return nil, nil
	}

	pkg, _ := resolveRefTarget(s, filePath, token)
	if pkg == "" {
		return nil, nil
	}

	// Return the range and placeholder so the editor pre-fills the rename box.
	r := tokenRange(text, line, char)
	return protocol.RangeWithPlaceholder{
		Range:       toProtocolRange(r),
		Placeholder: token,
	}, nil
}

// textDocumentRename renames a slice across all YAML files that reference it.
//
// newName may be:
//   - A bare slice name ("shared_libs") — renames within the same package.
//   - A full pkg_slice reference ("libc6_shared_libs") — must keep the same
//     package; cross-package rename is not supported.
func (s *Server) textDocumentRename(_ *glsp.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	if s.idx == nil {
		return nil, nil
	}

	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil, nil
	}

	text, ok := s.getDoc(filePath)
	if !ok {
		data, readErr := os.ReadFile(filePath)
		if readErr != nil {
			return nil, nil
		}
		text = string(data)
	}

	line := int(params.Position.Line)
	char := int(params.Position.Character)

	token := wordAtPosition(text, line, char)
	if token == "" {
		return nil, nil
	}

	srcPkg, srcSlice := resolveRefTarget(s, filePath, token)
	if srcPkg == "" {
		return nil, nil
	}

	// Resolve the new slice name from params.NewName.
	// Accept either "pkg_newSlice" (user left the package prefix in) or just "newSlice".
	newSlice := params.NewName
	if prefix := srcPkg + "_"; strings.HasPrefix(newSlice, prefix) {
		newSlice = newSlice[len(prefix):]
	}
	if newSlice == "" {
		return nil, fmt.Errorf("invalid new name %q", params.NewName)
	}

	changes := make(map[protocol.DocumentUri][]protocol.TextEdit)

	addEdit := func(fileURI protocol.DocumentUri, r parser.Range, newText string) {
		changes[fileURI] = append(changes[fileURI], protocol.TextEdit{
			Range:   toProtocolRange(r),
			NewText: newText,
		})
	}

	// Edit the slice definition (rename the key in the slices: map).
	is := s.idx.LookupSlice(srcPkg, srcSlice)
	if is != nil {
		addEdit(filePathToURI(is.File), is.Def.NameRange, newSlice)
	}

	// Edit all essential-list references across the workspace.
	newRef := srcPkg + "_" + newSlice
	for _, ref := range s.idx.FindReferences(srcPkg, srcSlice) {
		addEdit(filePathToURI(ref.File), ref.Range, newRef)
	}

	if len(changes) == 0 {
		return nil, nil
	}
	return &protocol.WorkspaceEdit{Changes: changes}, nil
}

// tokenRange returns the Range of the word returned by wordAtPosition.
func tokenRange(text string, line, char int) parser.Range {
	token := wordAtPosition(text, line, char)
	if token == "" {
		return parser.Range{}
	}
	lines := strings.Split(text, "\n")
	if line >= len(lines) {
		return parser.Range{}
	}
	row := lines[line]
	if char > len(row) {
		char = len(row)
	}
	start := char
	for start > 0 && isWordChar(row[start-1]) {
		start--
	}
	return parser.Range{
		Start: parser.Position{Line: line, Character: start},
		End:   parser.Position{Line: line, Character: start + len(token)},
	}
}
