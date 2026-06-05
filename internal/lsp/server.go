// Package lsp implements the chisel-releases Language Server using glsp.
package lsp

import (
	"fmt"
	"os"
	"sync"

	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glspserver "github.com/tliron/glsp/server"

	"github.com/canonical/chisel-releases-lsp/internal/index"
	"github.com/canonical/chisel-releases-lsp/internal/parser"
)

const lsName = "chisel-releases-lsp"

// Server is the LSP server instance.
type Server struct {
	handler protocol.Handler
	glspSrv *glspserver.Server
	idx     *index.Index
	rootURI string

	// docMu / docs holds the latest text of open documents (keyed by file path).
	docMu sync.RWMutex
	docs  map[string]string
}

// New creates a Server.
func New() *Server {
	s := &Server{
		docs: make(map[string]string),
	}
	s.handler = protocol.Handler{
		Initialize:             s.initialize,
		Initialized:            s.initialized,
		Shutdown:               s.shutdown,
		TextDocumentDidOpen:    s.textDocumentDidOpen,
		TextDocumentDidChange:  s.textDocumentDidChange,
		TextDocumentDidSave:    s.textDocumentDidSave,
		TextDocumentDidClose:   s.textDocumentDidClose,
		TextDocumentCompletion: s.textDocumentCompletion,
		TextDocumentDefinition: s.textDocumentDefinition,
		TextDocumentHover:      s.textDocumentHover,
	}
	return s
}

// RunStdio starts the server on stdin/stdout (blocking).
func (s *Server) RunStdio() error {
	commonlog.Configure(1, nil)
	s.glspSrv = glspserver.NewServer(&s.handler, lsName, false)
	return s.glspSrv.RunStdio()
}

func (s *Server) initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	if params.RootURI != nil {
		s.rootURI = string(*params.RootURI)
	} else if params.RootPath != nil {
		s.rootURI = "file://" + *params.RootPath
	}

	root, err := uriToPath(s.rootURI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chisel-releases-lsp: bad root URI %q: %v\n", s.rootURI, err)
	} else {
		idx, idxErr := index.New(root, func(filePath string) {
			s.publishDiagnosticsForFile(ctx, filePath)
		})
		if idxErr != nil {
			fmt.Fprintf(os.Stderr, "chisel-releases-lsp: index error: %v\n", idxErr)
		} else {
			s.idx = idx
		}
	}

	syncKind := protocol.TextDocumentSyncKindFull
	trueVal := true

	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: protocol.TextDocumentSyncOptions{
				OpenClose: &trueVal,
				Change:    &syncKind,
				Save:      &protocol.SaveOptions{IncludeText: &trueVal},
			},
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{"-", " "},
			},
			DefinitionProvider: &trueVal,
			HoverProvider:      &trueVal,
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    lsName,
			Version: strPtr("0.1.0"),
		},
	}, nil
}

func (s *Server) initialized(_ *glsp.Context, _ *protocol.InitializedParams) error {
	return nil
}

func (s *Server) shutdown(_ *glsp.Context) error {
	if s.idx != nil {
		s.idx.Close()
	}
	return nil
}

func (s *Server) textDocumentDidOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil
	}
	s.setDoc(filePath, params.TextDocument.Text)
	s.reindexAndPublish(ctx, filePath, []byte(params.TextDocument.Text))
	return nil
}

func (s *Server) textDocumentDidChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil
	}
	change, ok := params.ContentChanges[0].(protocol.TextDocumentContentChangeEventWhole)
	if !ok {
		return nil
	}
	s.setDoc(filePath, change.Text)
	s.reindexAndPublish(ctx, filePath, []byte(change.Text))
	return nil
}

func (s *Server) textDocumentDidSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil
	}
	if params.Text != nil {
		s.setDoc(filePath, *params.Text)
		s.reindexAndPublish(ctx, filePath, []byte(*params.Text))
	} else if s.idx != nil {
		_ = s.idx.IndexFile(filePath)
		s.publishDiagnosticsForFile(ctx, filePath)
	}
	return nil
}

func (s *Server) reindexAndPublish(ctx *glsp.Context, filePath string, content []byte) {
	if s.idx == nil {
		return
	}
	sf, err := parser.ParseBytes(content)
	if err != nil {
		publishDiagnostics(ctx, filePathToURI(filePath), []protocol.Diagnostic{
			{
				Range:    protocol.Range{},
				Severity: severityPtr(protocol.DiagnosticSeverityError),
				Source:   strPtr("chisel-releases-lsp"),
				Message:  "YAML parse error: " + err.Error(),
			},
		})
		return
	}
	s.idx.UpdateFile(filePath, sf)
	s.publishDiagnosticsForFile(ctx, filePath)
}

func (s *Server) setDoc(filePath, text string) {
	s.docMu.Lock()
	s.docs[filePath] = text
	s.docMu.Unlock()
}

func (s *Server) getDoc(filePath string) (string, bool) {
	s.docMu.RLock()
	defer s.docMu.RUnlock()
	t, ok := s.docs[filePath]
	return t, ok
}

func (s *Server) textDocumentDidClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil
	}
	s.docMu.Lock()
	delete(s.docs, filePath)
	s.docMu.Unlock()
	// Clear diagnostics so the client doesn't show stale squiggles.
	publishDiagnostics(ctx, params.TextDocument.URI, []protocol.Diagnostic{})
	return nil
}

