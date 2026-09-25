// handler.go implements the go-lsp server framework adapter for the MARTe LSP.
// It wraps all existing Handle* business-logic functions so they are callable
// via the standard go-lsp handler interfaces, while keeping the legacy
// HandleMessage / HandleHover / HandleDefinition / … API intact for tests.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	golsp "github.com/owenrumney/go-lsp/lsp"
	golspserver "github.com/owenrumney/go-lsp/server"

	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/lsp/cache"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// RunServer starts the LSP server using the go-lsp framework over stdio.
// It replaces the hand-rolled JSON-RPC loop that was previously here.
func RunServer() {
	SynchronousValidation = false
	GlobalSession = cache.NewSession("default")

	handler := &marteHandler{}
	srv := golspserver.NewServer(handler)

	// The go-lsp library has two defects around lifecycle requests that show up
	// in editors as "language server failed to terminate gracefully":
	//
	//  1. It marshals a nil result with `omitempty`, producing a response
	//     without "result" or "error" -- invalid JSON-RPC 2.0, which clients
	//     reject. Reply with an explicit null instead.
	//  2. Its own "exit" handler only returns an error, and notification
	//     errors are swallowed, so the process never terminates. The spec
	//     requires the server to exit, and a lingering process would keep
	//     serving the old, stale state after an editor restart.
	//
	// Both are registered as custom handlers, which the library applies after
	// its built-ins (overriding them).
	srv.HandleMethod("shutdown", func(ctx context.Context, _ json.RawMessage) (any, error) {
		stopAllValidations()
		return json.RawMessage("null"), nil
	})
	srv.HandleNotification("exit", func(ctx context.Context, _ json.RawMessage) error {
		stopAllValidations()
		os.Exit(0)
		return nil
	})

	if err := srv.Run(context.Background(), golspserver.RunStdio()); err != nil && !isCleanShutdown(err) {
		logger.Errorf("LSP server exited: %v", err)
	}
}

// isCleanShutdown reports whether err is just the client closing the
// connection. Editors treat everything on stderr as an error, so a normal
// exit must not be logged as one.
func isCleanShutdown(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") || strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "connection reset by peer")
}

// marteHandler implements all go-lsp server handler interfaces.
// Each method converts types and delegates to the existing Handle* functions.
type marteHandler struct{}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

func (h *marteHandler) Initialize(ctx context.Context, params *golsp.InitializeParams) (*golsp.InitializeResult, error) {
	defer watchRequest("initialize")()
	root := ""
	if params.RootURI != nil && *params.RootURI != "" {
		root = uriToPath(string(*params.RootURI))
	} else if params.RootPath != nil && *params.RootPath != "" {
		root = *params.RootPath
	}

	if root != "" {
		view := GlobalSession.CreateView("main", root)
		snap := view.Snapshot()
		logger.Debugf("Scanning workspace: %s", root)
		done := watchOperation("workspace scan of " + root)
		if err := snap.Tree().ScanDirectory(root); err != nil {
			logger.Debugf("ScanDirectory failed: %v", err)
		}
		snap.Tree().ResolveReferences(nil)
		snap.Tree().ResolveFields(nil)
		view.SetSnapshot(snap)
		GlobalSchema = schema.LoadFullSchema(root)
		done()
		logger.Debugf("Workspace ready")

		// Trigger initial workspace-wide validation in the background.
		go func() {
			view := GlobalSession.ViewOf("")
			if view != nil {
				runValidation(context.Background(), "", view.Snapshot())
			}
		}()
	}

	return &golsp.InitializeResult{
		ServerInfo: &golsp.ServerInfo{Name: "mdt", Version: "0.1.0"},
		// go-lsp auto-detects most capabilities from implemented interfaces.
		// We only need to set options that are not auto-detectable.
		Capabilities: golsp.ServerCapabilities{
			CompletionProvider: &golsp.CompletionOptions{
				TriggerCharacters: []string{"=", " ", "@"},
			},
		},
	}, nil
}

func (h *marteHandler) Shutdown(ctx context.Context) error {
	defer watchRequest("shutdown")()
	return nil
}

// ─── Client reference (for publishing diagnostics) ────────────────────────────

func (h *marteHandler) SetClient(client *golspserver.Client) {
	defer watchRequest("SetClient")()
	// Wire the go-lsp client into the diagnostic publishing path so that
	// runValidation / publishImmediateDiagnostics use the proper channel
	// instead of writing raw JSON-RPC to stdout.
	PublishDiagnosticsFn = func(ctx context.Context, fileURI string, diags []LSPDiagnostic) {
		golspDiags := make([]golsp.Diagnostic, len(diags))
		for i, d := range diags {
			d.Range = clampRange(d.Range)
			sev := golsp.DiagnosticSeverity(d.Severity)
			golspDiags[i] = golsp.Diagnostic{
				Range: golsp.Range{
					Start: gp(d.Range.Start.Line, d.Range.Start.Character),
					End:   gp(d.Range.End.Line, d.Range.End.Character),
				},
				Severity: &sev,
				Message:  d.Message,
				Source:   d.Source,
			}
		}
		if err := client.PublishDiagnostics(ctx, &golsp.PublishDiagnosticsParams{
			URI:         golsp.DocumentURI(fileURI),
			Diagnostics: golspDiags,
		}); err != nil {
			logger.Printf("PublishDiagnostics error: %v\n", err)
		}
	}
}

// ─── Text Document Sync ───────────────────────────────────────────────────────

func (h *marteHandler) DidOpen(ctx context.Context, params *golsp.DidOpenTextDocumentParams) error {
	defer watchRequest("textDocument/didOpen")()
	HandleDidOpen(DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:  string(params.TextDocument.URI),
			Text: params.TextDocument.Text,
		},
	})
	return nil
}

func (h *marteHandler) DidChange(ctx context.Context, params *golsp.DidChangeTextDocumentParams) error {
	defer watchRequest("textDocument/didChange")()
	changes := make([]TextDocumentContentChangeEvent, len(params.ContentChanges))
	for i, c := range params.ContentChanges {
		ch := TextDocumentContentChangeEvent{Text: c.Text}
		if c.Range != nil {
			r := Range{
				Start: Position{Line: c.Range.Start.Line, Character: c.Range.Start.Character},
				End:   Position{Line: c.Range.End.Line, Character: c.Range.End.Character},
			}
			ch.Range = &r
		}
		changes[i] = ch
	}
	HandleDidChange(DidChangeTextDocumentParams{
		TextDocument: VersionedTextDocumentIdentifier{
			URI:     string(params.TextDocument.URI),
			Version: params.TextDocument.Version,
		},
		ContentChanges: changes,
	})
	return nil
}

func (h *marteHandler) DidClose(ctx context.Context, params *golsp.DidCloseTextDocumentParams) error {
	defer watchRequest("textDocument/didClose")()
	HandleDidClose(DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
	})
	return nil
}

// ─── Language Features ────────────────────────────────────────────────────────

func (h *marteHandler) Hover(ctx context.Context, params *golsp.HoverParams) (*golsp.Hover, error) {
	defer watchRequest("textDocument/hover")()
	res := HandleHover(HoverParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	if res == nil {
		return nil, nil
	}
	var mc golsp.MarkupContent
	switch c := res.Contents.(type) {
	case MarkupContent:
		mc = golsp.MarkupContent{Kind: golsp.MarkupKind(c.Kind), Value: c.Value}
	case string:
		mc = golsp.MarkupContent{Kind: golsp.PlainText, Value: c}
	}
	return &golsp.Hover{Contents: mc}, nil
}

func (h *marteHandler) Definition(ctx context.Context, params *golsp.DefinitionParams) ([]golsp.Location, error) {
	defer watchRequest("textDocument/definition")()
	res := HandleDefinition(DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	return convertLocationsResult(res), nil
}

func (h *marteHandler) TypeDefinition(ctx context.Context, params *golsp.TypeDefinitionParams) ([]golsp.Location, error) {
	defer watchRequest("textDocument/typeDefinition")()
	res := HandleTypeDefinition(TypeDefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	return convertLocationsResult(res), nil
}

func (h *marteHandler) References(ctx context.Context, params *golsp.ReferenceParams) ([]golsp.Location, error) {
	defer watchRequest("textDocument/references")()
	locs := HandleReferences(ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
		Context:      ReferenceContext{IncludeDeclaration: params.Context.IncludeDeclaration},
	})
	return convertLocations(locs), nil
}

func (h *marteHandler) Completion(ctx context.Context, params *golsp.CompletionParams) (*golsp.CompletionList, error) {
	defer watchRequest("textDocument/completion")()
	var triggerCtx CompletionContext
	if params.Context != nil {
		triggerCtx = CompletionContext{TriggerKind: int(params.Context.TriggerKind)}
	}
	res := HandleCompletion(CompletionParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
		Context:      triggerCtx,
	})
	if res == nil {
		return nil, nil
	}
	items := make([]golsp.CompletionItem, len(res.Items))
	for i, item := range res.Items {
		kind := golsp.CompletionItemKind(item.Kind)
		itf := golsp.InsertTextFormat(item.InsertTextFormat)
		ci := golsp.CompletionItem{
			Label:            item.Label,
			Kind:             &kind,
			Detail:           item.Detail,
			InsertText:       item.InsertText,
			InsertTextFormat: &itf,
			SortText:         item.SortText,
		}
		if item.Documentation != "" {
			ci.Documentation = &golsp.MarkupContent{Kind: golsp.PlainText, Value: item.Documentation}
		}
		items[i] = ci
	}
	return &golsp.CompletionList{IsIncomplete: res.IsIncomplete, Items: items}, nil
}

func (h *marteHandler) Formatting(ctx context.Context, params *golsp.DocumentFormattingParams) ([]golsp.TextEdit, error) {
	defer watchRequest("textDocument/formatting")()
	edits := HandleFormatting(DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
	})
	return convertTextEdits(edits), nil
}

func (h *marteHandler) Rename(ctx context.Context, params *golsp.RenameParams) (*golsp.WorkspaceEdit, error) {
	defer watchRequest("textDocument/rename")()
	res := HandleRename(RenameParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
		NewName:      params.NewName,
	})
	if res == nil {
		return nil, nil
	}
	changes := make(map[golsp.DocumentURI][]golsp.TextEdit, len(res.Changes))
	for uri, edits := range res.Changes {
		changes[golsp.DocumentURI(uri)] = convertTextEdits(edits)
	}
	return &golsp.WorkspaceEdit{Changes: changes}, nil
}

func (h *marteHandler) InlayHint(ctx context.Context, params *golsp.InlayHintParams) ([]golsp.InlayHint, error) {
	defer watchRequest("textDocument/inlayHint")()
	hints := HandleInlayHint(InlayHintParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Range: Range{
			Start: Position{Line: params.Range.Start.Line, Character: params.Range.Start.Character},
			End:   Position{Line: params.Range.End.Line, Character: params.Range.End.Character},
		},
	})
	result := make([]golsp.InlayHint, len(hints))
	for i, hint := range hints {
		kind := golsp.InlayHintKind(hint.Kind)
		labelJSON, _ := json.Marshal(hint.Label)
		result[i] = golsp.InlayHint{
			Position: gp(hint.Position.Line, hint.Position.Character),
			Label:    labelJSON,
			Kind:     &kind,
		}
	}
	return result, nil
}

func (h *marteHandler) DocumentSymbol(ctx context.Context, params *golsp.DocumentSymbolParams) ([]golsp.DocumentSymbol, error) {
	defer watchRequest("textDocument/documentSymbol")()
	syms := HandleDocumentSymbol(DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
	})
	return convertDocumentSymbols(syms), nil
}

func (h *marteHandler) WorkspaceSymbol(ctx context.Context, params *golsp.WorkspaceSymbolParams) ([]golsp.SymbolInformation, error) {
	defer watchRequest("workspace/symbol")()
	syms := HandleWorkspaceSymbol(WorkspaceSymbolParams{Query: params.Query})
	result := make([]golsp.SymbolInformation, len(syms))
	for i, s := range syms {
		result[i] = golsp.SymbolInformation{
			Name: s.Name,
			Kind: golsp.SymbolKind(s.Kind),
			Location: golsp.Location{
				URI: golsp.DocumentURI(s.Location.URI),
				Range: golsp.Range{
					Start: gp(s.Location.Range.Start.Line, s.Location.Range.Start.Character),
					End:   gp(s.Location.Range.End.Line, s.Location.Range.End.Character),
				},
			},
			ContainerName: s.ContainerName,
		}
	}
	return result, nil
}

func (h *marteHandler) CodeAction(ctx context.Context, params *golsp.CodeActionParams) ([]golsp.CodeAction, error) {
	defer watchRequest("textDocument/codeAction")()
	// Convert incoming diagnostics from go-lsp → internal representation.
	diags := make([]LSPDiagnostic, len(params.Context.Diagnostics))
	for i, d := range params.Context.Diagnostics {
		sev := 0
		if d.Severity != nil {
			sev = int(*d.Severity)
		}
		diags[i] = LSPDiagnostic{
			Range: Range{
				Start: Position{Line: d.Range.Start.Line, Character: d.Range.Start.Character},
				End:   Position{Line: d.Range.End.Line, Character: d.Range.End.Character},
			},
			Severity: sev,
			Message:  d.Message,
			Source:   d.Source,
		}
	}
	actions := HandleCodeAction(CodeActionParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Range: Range{
			Start: Position{Line: params.Range.Start.Line, Character: params.Range.Start.Character},
			End:   Position{Line: params.Range.End.Line, Character: params.Range.End.Character},
		},
		Context: CodeActionContext{Diagnostics: diags},
	})
	result := make([]golsp.CodeAction, len(actions))
	for i, a := range actions {
		kind := golsp.CodeActionKind(a.Kind)
		ga := golsp.CodeAction{
			Title: a.Title,
			Kind:  &kind,
		}
		if a.Edit != nil {
			changes := make(map[golsp.DocumentURI][]golsp.TextEdit, len(a.Edit.Changes))
			for uri, edits := range a.Edit.Changes {
				changes[golsp.DocumentURI(uri)] = convertTextEdits(edits)
			}
			ga.Edit = &golsp.WorkspaceEdit{Changes: changes}
		}
		result[i] = ga
	}
	return result, nil
}

// ─── Call Hierarchy ───────────────────────────────────────────────────────────

func (h *marteHandler) PrepareCallHierarchy(ctx context.Context, params *golsp.CallHierarchyPrepareParams) ([]golsp.CallHierarchyItem, error) {
	defer watchRequest("textDocument/prepareCallHierarchy")()
	items := HandlePrepareCallHierarchy(CallHierarchyPrepareParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	return convertCallHierarchyItemsTo(items), nil
}

func (h *marteHandler) IncomingCalls(ctx context.Context, params *golsp.CallHierarchyIncomingCallsParams) ([]golsp.CallHierarchyIncomingCall, error) {
	defer watchRequest("callHierarchy/incomingCalls")()
	internalParams := CallHierarchyIncomingCallsParams{
		Item: convertCallHierarchyItemFrom(params.Item),
	}
	calls := HandleIncomingCalls(internalParams)
	result := make([]golsp.CallHierarchyIncomingCall, len(calls))
	for i, c := range calls {
		result[i] = golsp.CallHierarchyIncomingCall{
			From:       convertCallHierarchyItemTo(c.From),
			FromRanges: convertRanges(c.FromRanges),
		}
	}
	return result, nil
}

func (h *marteHandler) OutgoingCalls(ctx context.Context, params *golsp.CallHierarchyOutgoingCallsParams) ([]golsp.CallHierarchyOutgoingCall, error) {
	defer watchRequest("callHierarchy/outgoingCalls")()
	internalParams := CallHierarchyOutgoingCallsParams{
		Item: convertCallHierarchyItemFrom(params.Item),
	}
	calls := HandleOutgoingCalls(internalParams)
	result := make([]golsp.CallHierarchyOutgoingCall, len(calls))
	for i, c := range calls {
		result[i] = golsp.CallHierarchyOutgoingCall{
			To:         convertCallHierarchyItemTo(c.To),
			FromRanges: convertRanges(c.FromRanges),
		}
	}
	return result, nil
}

// ─── Compile-time interface assertions ────────────────────────────────────────

var (
	_ golspserver.LifecycleHandler          = (*marteHandler)(nil)
	_ golspserver.ClientHandler             = (*marteHandler)(nil)
	_ golspserver.TextDocumentSyncHandler   = (*marteHandler)(nil)
	_ golspserver.HoverHandler              = (*marteHandler)(nil)
	_ golspserver.DefinitionHandler         = (*marteHandler)(nil)
	_ golspserver.TypeDefinitionHandler     = (*marteHandler)(nil)
	_ golspserver.ReferencesHandler         = (*marteHandler)(nil)
	_ golspserver.CompletionHandler         = (*marteHandler)(nil)
	_ golspserver.DocumentFormattingHandler = (*marteHandler)(nil)
	_ golspserver.RenameHandler             = (*marteHandler)(nil)
	_ golspserver.InlayHintHandler          = (*marteHandler)(nil)
	_ golspserver.DocumentSymbolHandler     = (*marteHandler)(nil)
	_ golspserver.WorkspaceSymbolHandler    = (*marteHandler)(nil)
	_ golspserver.CodeActionHandler         = (*marteHandler)(nil)
	_ golspserver.CallHierarchyHandler      = (*marteHandler)(nil)
)

// ─── Type conversion helpers ─────────────────────────────────────────────────

// gp converts internal LSP coordinates into wire coordinates, clamping
// negatives. Internal positions are 0 for entities that have no source
// location -- the package fragment, nodes generated by with/template/foreach
// expansion -- and converting them blindly produced line/character -1. Clients
// validate those as unsigned and reject the whole message with
// "invalid value: integer `-1`, expected u32", silently dropping the response
// (or the diagnostics notification).
func gp(line, character int) golsp.Position {
	if line < 0 {
		line = 0
	}
	if character < 0 {
		character = 0
	}
	return golsp.Position{Line: line, Character: character}
}

func convertLocationsResult(res any) []golsp.Location {
	if res == nil {
		return nil
	}
	if locs, ok := res.([]Location); ok {
		return convertLocations(locs)
	}
	return nil
}

func convertLocations(locs []Location) []golsp.Location {
	result := make([]golsp.Location, len(locs))
	for i, l := range locs {
		result[i] = golsp.Location{
			URI: golsp.DocumentURI(l.URI),
			Range: golsp.Range{
				Start: gp(l.Range.Start.Line, l.Range.Start.Character),
				End:   gp(l.Range.End.Line, l.Range.End.Character),
			},
		}
	}
	return result
}

func convertTextEdits(edits []TextEdit) []golsp.TextEdit {
	result := make([]golsp.TextEdit, len(edits))
	for i, e := range edits {
		result[i] = golsp.TextEdit{
			Range: golsp.Range{
				Start: gp(e.Range.Start.Line, e.Range.Start.Character),
				End:   gp(e.Range.End.Line, e.Range.End.Character),
			},
			NewText: e.NewText,
		}
	}
	return result
}

func convertDocumentSymbols(syms []DocumentSymbol) []golsp.DocumentSymbol {
	result := make([]golsp.DocumentSymbol, len(syms))
	for i, s := range syms {
		result[i] = golsp.DocumentSymbol{
			Name:   s.Name,
			Detail: s.Detail,
			Kind:   golsp.SymbolKind(s.Kind),
			Range: golsp.Range{
				Start: gp(s.Range.Start.Line, s.Range.Start.Character),
				End:   gp(s.Range.End.Line, s.Range.End.Character),
			},
			SelectionRange: golsp.Range{
				Start: gp(s.SelectionRange.Start.Line, s.SelectionRange.Start.Character),
				End:   gp(s.SelectionRange.End.Line, s.SelectionRange.End.Character),
			},
			Children: convertDocumentSymbols(s.Children),
		}
	}
	return result
}

func convertCallHierarchyItemsTo(items []CallHierarchyItem) []golsp.CallHierarchyItem {
	result := make([]golsp.CallHierarchyItem, len(items))
	for i, item := range items {
		result[i] = convertCallHierarchyItemTo(item)
	}
	return result
}

func convertCallHierarchyItemTo(item CallHierarchyItem) golsp.CallHierarchyItem {
	dataJSON, _ := json.Marshal(item.Data)
	return golsp.CallHierarchyItem{
		Name:   item.Name,
		Kind:   golsp.SymbolKind(item.Kind),
		Detail: item.Detail,
		URI:    golsp.DocumentURI(item.URI),
		Range: golsp.Range{
			Start: gp(item.Range.Start.Line, item.Range.Start.Character),
			End:   gp(item.Range.End.Line, item.Range.End.Character),
		},
		SelectionRange: golsp.Range{
			Start: gp(item.SelectionRange.Start.Line, item.SelectionRange.Start.Character),
			End:   gp(item.SelectionRange.End.Line, item.SelectionRange.End.Character),
		},
		Data: dataJSON,
	}
}

func convertCallHierarchyItemFrom(item golsp.CallHierarchyItem) CallHierarchyItem {
	// The Data field stores the node name as a JSON string; unmarshal it so
	// the internal handlers can do type-assert to string.
	var data string
	if item.Data != nil {
		_ = json.Unmarshal(item.Data, &data)
	}
	return CallHierarchyItem{
		Name:   item.Name,
		Kind:   SymbolKind(item.Kind),
		Detail: item.Detail,
		URI:    string(item.URI),
		Range: Range{
			Start: Position{Line: item.Range.Start.Line, Character: item.Range.Start.Character},
			End:   Position{Line: item.Range.End.Line, Character: item.Range.End.Character},
		},
		SelectionRange: Range{
			Start: Position{Line: item.SelectionRange.Start.Line, Character: item.SelectionRange.Start.Character},
			End:   Position{Line: item.SelectionRange.End.Line, Character: item.SelectionRange.End.Character},
		},
		Data: data,
	}
}

func convertRanges(ranges []Range) []golsp.Range {
	result := make([]golsp.Range, len(ranges))
	for i, r := range ranges {
		result[i] = golsp.Range{
			Start: gp(r.Start.Line, r.Start.Character),
			End:   gp(r.End.Line, r.End.Character),
		}
	}
	return result
}
