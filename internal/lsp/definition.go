package lsp

import (
	"os"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) textDocumentDefinition(_ *glsp.Context, params *protocol.DefinitionParams) (any, error) {
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

	pkg, sliceName := splitSliceRef(token)
	if pkg == "" {
		return nil, nil
	}

	is := s.idx.LookupSlice(pkg, sliceName)
	if is == nil {
		return nil, nil
	}

	return protocol.Location{
		URI:   filePathToURI(is.File),
		Range: toProtocolRange(is.Def.NameRange),
	}, nil
}
