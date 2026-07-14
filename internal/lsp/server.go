// Package lsp implements the chisel-releases Language Server using glsp.
package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/tliron/commonlog"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glspserver "github.com/tliron/glsp/server"

	"github.com/dariuszd21/chisel-releases-lsp/internal/index"
	"github.com/dariuszd21/chisel-releases-lsp/internal/parser"
)

const lsName = "chisel-releases-lsp"

const defaultMinPrefixLen = 2

// Config holds runtime-configurable server settings.
type Config struct {
	// MinPrefixLen is the minimum number of characters the user must type in
	// an essential reference before completions are offered.  Minimum value
	// is 2 (values lower than 2 are raised to 2).
	// Corresponds to the LSP setting key "minPrefixLength".
	MinPrefixLen int
}

// defaultConfig returns a Config populated with sane defaults.
func defaultConfig() Config {
	return Config{MinPrefixLen: defaultMinPrefixLen}
}

// Server is the LSP server instance.
type Server struct {
	handler protocol.Handler
	glspSrv *glspserver.Server
	idx     *index.Index
	rootURI string

	// docMu / docs holds the latest text of open documents (keyed by file path).
	docMu sync.RWMutex
	docs  map[string]string

	// diagCacheMu guards diagLineCode, which caches the line→diagCode map
	// produced by the most recent computeDiagnostics run for each file.
	// computeCodeActions reads this cache instead of re-running all diagnostics.
	diagCacheMu  sync.RWMutex
	diagLineCode map[string]map[uint32]string

	// notifyMu guards notifier; notifier is set from the first request context and
	// used by background goroutines that must send LSP notifications without a
	// request-scoped context.
	notifyMu sync.Mutex
	notifier Notifier

	// configMu guards config which may be updated by workspace/didChangeConfiguration.
	configMu sync.RWMutex
	config   Config
}

// New creates a Server.
func New() *Server {
	s := &Server{
		docs:         make(map[string]string),
		diagLineCode: make(map[string]map[uint32]string),
		config:       defaultConfig(),
	}
	s.handler = protocol.Handler{
		Initialize:                      s.initialize,
		Initialized:                     s.initialized,
		Shutdown:                        s.shutdown,
		TextDocumentDidOpen:             s.textDocumentDidOpen,
		TextDocumentDidChange:           s.textDocumentDidChange,
		TextDocumentDidSave:             s.textDocumentDidSave,
		TextDocumentDidClose:            s.textDocumentDidClose,
		TextDocumentCompletion:          s.textDocumentCompletion,
		TextDocumentDefinition:          s.textDocumentDefinition,
		TextDocumentHover:               s.textDocumentHover,
		TextDocumentReferences:          s.textDocumentReferences,
		TextDocumentDocumentSymbol:      s.textDocumentDocumentSymbol,
		WorkspaceSymbol:                 s.workspaceSymbol,
		TextDocumentRename:              s.textDocumentRename,
		TextDocumentPrepareRename:       s.textDocumentPrepareRename,
		TextDocumentCodeAction:          s.textDocumentCodeAction,
		WorkspaceDidChangeConfiguration: s.workspaceDidChangeConfiguration,
		WorkspaceExecuteCommand:         s.workspaceExecuteCommand,
		WorkspaceDidChangeWatchedFiles:  s.workspaceDidChangeWatchedFiles,
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

	// Apply any settings passed in initializationOptions.
	if params.InitializationOptions != nil {
		s.applySettings(params.InitializationOptions)
	}

	root, err := uriToPath(s.rootURI)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chisel-releases-lsp: bad root URI %q: %v\n", s.rootURI, err)
	} else {
		// Store the notifier from this ctx before spawning the index so that
		// background callbacks never capture a stale request-scoped context.
		s.storeNotifier(ctx)
		idx, idxErr := index.New(root,
			func(filePath string) { s.publishDiagnosticsBackground(filePath) },
			func(filePath string) { s.clearDiagnosticsBackground(filePath) },
		)
		if idxErr != nil {
			fmt.Fprintf(os.Stderr, "chisel-releases-lsp: index error: %v\n", idxErr)
		} else {
			s.idx = idx
			// Push initial diagnostics for all indexed files in the background
			// so the editor's Problems panel shows workspace-wide issues
			// immediately, without waiting for each file to be opened.
			go s.publishAllFileDiagnostics()
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
				// "_" triggers completion popup in editors after "pkg_" is typed.
				// The "-" trigger is intentionally omitted: it would fire with
				// prefix="" (0 chars), which is always below the minPrefixLength
				// threshold and would produce an empty list.
				TriggerCharacters: []string{"_"},
			},
			DefinitionProvider:      &trueVal,
			HoverProvider:           &trueVal,
			ReferencesProvider:      &trueVal,
			DocumentSymbolProvider:  &trueVal,
			WorkspaceSymbolProvider: &trueVal,
			RenameProvider:          protocol.RenameOptions{PrepareProvider: &trueVal},
			CodeActionProvider: &protocol.CodeActionOptions{
				CodeActionKinds: []protocol.CodeActionKind{protocol.CodeActionKindQuickFix},
			},
			ExecuteCommandProvider: &protocol.ExecuteCommandOptions{
				Commands: []string{CmdGotoConflict, CmdGotoFirstOccurrence},
			},
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
	s.storeNotifier(ctx)
	n := ctxNotifier{ctx}
	s.setDoc(filePath, params.TextDocument.Text)
	s.reindexAndPublish(n, filePath, []byte(params.TextDocument.Text))
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
	s.storeNotifier(ctx)
	n := ctxNotifier{ctx}
	s.setDoc(filePath, change.Text)
	s.reindexAndPublish(n, filePath, []byte(change.Text))
	return nil
}

func (s *Server) textDocumentDidSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	filePath, err := uriToPath(string(params.TextDocument.URI))
	if err != nil {
		return nil
	}
	s.storeNotifier(ctx)
	n := ctxNotifier{ctx}
	if params.Text != nil {
		s.setDoc(filePath, *params.Text)
		s.reindexAndPublish(n, filePath, []byte(*params.Text))
	} else if s.idx != nil {
		if idxErr := s.idx.IndexFile(filePath); idxErr != nil {
			publishDiagnostics(n, filePathToURI(filePath), []protocol.Diagnostic{
				{
					Range:    protocol.Range{},
					Severity: severityPtr(protocol.DiagnosticSeverityError),
					Source:   strPtr("chisel-releases-lsp"),
					Message:  "YAML parse error: " + idxErr.Error(),
				},
			})
		} else {
			s.publishDiagnosticsForFile(n, filePath)
		}
	}
	return nil
}

func (s *Server) reindexAndPublish(n Notifier, filePath string, content []byte) {
	if s.idx == nil {
		return
	}
	sf, err := parser.ParseBytes(content)
	if err != nil {
		publishDiagnostics(n, filePathToURI(filePath), []protocol.Diagnostic{
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
	s.publishDiagnosticsForFile(n, filePath)
	// Republish all other open files: cross-file analysis (collision detection)
	// may produce different results for them after this file was updated.
	s.republishOpenFiles(n, filePath)
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
	s.storeNotifier(ctx)
	n := ctxNotifier{ctx}
	s.docMu.Lock()
	delete(s.docs, filePath)
	s.docMu.Unlock()

	if s.idx == nil {
		publishDiagnostics(n, params.TextDocument.URI, []protocol.Diagnostic{})
		return nil
	}

	// Revert to on-disk state so cross-file analysis reflects saved content,
	// not the unsaved in-memory buffer that was open in the editor.
	if idxErr := s.idx.IndexFile(filePath); idxErr != nil {
		// File is gone or broken on disk — clear its diagnostics.
		publishDiagnostics(n, params.TextDocument.URI, []protocol.Diagnostic{})
	} else {
		// Publish the real on-disk diagnostics and update open peers (collision
		// detection may now produce different results for them).
		s.publishDiagnosticsForFile(n, filePath)
		s.republishOpenFiles(n, filePath)
	}
	return nil
}

// workspaceDidChangeConfiguration handles the workspace/didChangeConfiguration
// notification sent by editors when the user changes settings.
func (s *Server) workspaceDidChangeConfiguration(_ *glsp.Context, params *protocol.DidChangeConfigurationParams) error {
	if params != nil {
		s.applySettings(params.Settings)
	}
	return nil
}

// workspaceDidChangeWatchedFiles handles workspace/didChangeWatchedFiles
// notifications sent by the editor's built-in file watcher. This supplements
// the server's own fsnotify watcher (e.g. on network filesystems where fsnotify
// may be unreliable). Operations are idempotent so duplicate events are harmless.
func (s *Server) workspaceDidChangeWatchedFiles(_ *glsp.Context, params *protocol.DidChangeWatchedFilesParams) error {
	if s.idx == nil || params == nil {
		return nil
	}
	s.notifyMu.Lock()
	n := s.notifier
	s.notifyMu.Unlock()
	for _, ev := range params.Changes {
		filePath, err := uriToPath(string(ev.URI))
		if err != nil {
			continue
		}
		switch ev.Type {
		case protocol.FileChangeTypeCreated, protocol.FileChangeTypeChanged:
			_ = s.idx.IndexFile(filePath)
			if n != nil {
				s.publishDiagnosticsForFile(n, filePath)
				s.republishOpenFiles(n, filePath)
			}
		case protocol.FileChangeTypeDeleted:
			s.idx.DeleteFile(filePath)
			if n != nil {
				// Clear diagnostics for the deleted file and update peers.
				publishDiagnostics(n, filePathToURI(filePath), []protocol.Diagnostic{})
				s.republishOpenFiles(n, filePath)
			}
		}
	}
	return nil
}

// workspaceExecuteCommand handles workspace/executeCommand requests.
// Supported commands: CmdGotoConflict, CmdGotoFirstOccurrence — both navigate
// the editor to a target file/range via a window/showDocument notification.
func (s *Server) workspaceExecuteCommand(ctx *glsp.Context, params *protocol.ExecuteCommandParams) (any, error) {
	switch params.Command {
	case CmdGotoConflict, CmdGotoFirstOccurrence:
	default:
		return nil, nil
	}
	// Arguments: [uri string, line float64, character float64]
	if len(params.Arguments) < 3 {
		return nil, nil
	}
	uri, ok1 := params.Arguments[0].(string)
	lineF, ok2 := params.Arguments[1].(float64) // JSON numbers unmarshal as float64
	charF, ok3 := params.Arguments[2].(float64)
	if !ok1 || !ok2 || !ok3 {
		return nil, nil
	}
	line := uint32(lineF)
	char := uint32(charF)
	sel := protocol.Range{
		Start: protocol.Position{Line: line, Character: char},
		End:   protocol.Position{Line: line, Character: char},
	}
	takeFocus := true
	external := false
	ctx.Notify(string(protocol.ServerWindowShowDocument), protocol.ShowDocumentParams{
		URI:       protocol.URI(uri),
		External:  &external,
		TakeFocus: &takeFocus,
		Selection: &sel,
	})
	return nil, nil
}

// applySettings extracts configuration values from v (which may be a
// map[string]any from JSON unmarshalling, or a json.RawMessage).
// Settings are read from either the top-level key "minPrefixLength" or
// the nested path "chiselReleasesLsp.minPrefixLength".
func (s *Server) applySettings(v any) {
	// Normalise: if it's a json.RawMessage or []byte, unmarshal first.
	if raw, ok := v.(json.RawMessage); ok {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err == nil {
			v = m
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	// Support both flat "minPrefixLength" and namespaced "chiselReleasesLsp.minPrefixLength".
	val, found := m["minPrefixLength"]
	if !found {
		if nested, ok := m["chiselReleasesLsp"].(map[string]any); ok {
			val, found = nested["minPrefixLength"]
		}
	}
	if !found {
		return
	}
	n := toInt(val)
	if n < defaultMinPrefixLen {
		n = defaultMinPrefixLen
	}
	s.configMu.Lock()
	s.config.MinPrefixLen = n
	s.configMu.Unlock()
}

// minPrefixLen returns the current configured minimum prefix length.
func (s *Server) minPrefixLen() int {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config.MinPrefixLen
}

// toInt coerces common JSON number types (float64, int, int64) to int.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// storeNotifier caches a notification sender from ctx for use by background goroutines.
// It keeps the first non-nil ctx seen, so background goroutines always have a valid channel.
func (s *Server) storeNotifier(ctx *glsp.Context) {
	if ctx == nil {
		return
	}
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	if s.notifier == nil {
		s.notifier = ctxNotifier{ctx}
	}
}

// publishAllFileDiagnostics pushes diagnostics for every file in the index.
// Called once at startup so the Problems panel shows workspace-wide issues
// without the user having to open each file individually.
func (s *Server) publishAllFileDiagnostics() {
	if s.idx == nil {
		return
	}
	s.notifyMu.Lock()
	n := s.notifier
	s.notifyMu.Unlock()
	if n == nil {
		return
	}
	for _, filePath := range s.idx.AllFiles() {
		diags := s.computeDiagnostics(filePath)
		if diags == nil {
			continue
		}
		n.Notify(protocol.ServerTextDocumentPublishDiagnostics,
			protocol.PublishDiagnosticsParams{
				URI:         filePathToURI(filePath),
				Diagnostics: diags,
			})
	}
}

// publishDiagnosticsBackground recomputes and sends diagnostics for filePath
// without requiring a request-scoped context. Used by the file watcher.
func (s *Server) publishDiagnosticsBackground(filePath string) {
	if s.idx == nil {
		return
	}
	sf := s.idx.FileSliceFile(filePath)
	if sf == nil {
		return
	}
	s.notifyMu.Lock()
	n := s.notifier
	s.notifyMu.Unlock()
	if n == nil {
		return
	}
	diags := s.computeDiagnostics(filePath)
	n.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         filePathToURI(filePath),
		Diagnostics: diags,
	})
	// Republish all other open files: cross-file analysis (collision detection)
	// may produce different results for them after this file changed.
	s.republishOpenFiles(n, filePath)
}

// clearDiagnosticsBackground sends an empty diagnostic list for filePath,
// clearing any squiggles the client may be showing for a deleted file.
func (s *Server) clearDiagnosticsBackground(filePath string) {
	s.notifyMu.Lock()
	n := s.notifier
	s.notifyMu.Unlock()
	if n == nil {
		return
	}
	n.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         filePathToURI(filePath),
		Diagnostics: []protocol.Diagnostic{},
	})
	// Republish all other open files: a deleted file may have resolved collisions.
	s.republishOpenFiles(n, filePath)
}

// republishOpenFiles recomputes and pushes diagnostics for every document
// currently open in the editor, skipping skipPath (already handled by the caller).
func (s *Server) republishOpenFiles(n Notifier, skipPath string) {
	s.docMu.RLock()
	open := make([]string, 0, len(s.docs))
	for f := range s.docs {
		if f != skipPath {
			open = append(open, f)
		}
	}
	s.docMu.RUnlock()

	for _, f := range open {
		diags := s.computeDiagnostics(f)
		if diags == nil {
			continue
		}
		n.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
			URI:         filePathToURI(f),
			Diagnostics: diags,
		})
	}
}
