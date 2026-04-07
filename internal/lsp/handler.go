// handler.go implements the go-lsp server framework adapter for the MARTe LSP.
// It wraps all existing Handle* business-logic functions so they are callable
// via the standard go-lsp handler interfaces, while keeping the legacy
// HandleMessage / HandleHover / HandleDefinition / … API intact for tests.
package lsp

import (
	"context"
	"encoding/json"

	golsp "github.com/owenrumney/go-lsp/lsp"
	golspserver "github.com/owenrumney/go-lsp/server"

	"github.com/marte-community/marte-dev-tools/internal/lsp/cache"
	"github.com/marte-community/marte-dev-tools/internal/logger"
	"github.com/marte-community/marte-dev-tools/internal/schema"
)

// RunServer starts the LSP server using the go-lsp framework over stdio.
// It replaces the hand-rolled JSON-RPC loop that was previously here.
func RunServer() {
	SynchronousValidation = false
	GlobalSession = cache.NewSession("default")

	handler := &marteHandler{}
	srv := golspserver.NewServer(handler)
	if err := srv.Run(context.Background(), golspserver.RunStdio()); err != nil {
		logger.Printf("LSP server exited: %v\n", err)
	}
}

// marteHandler implements all go-lsp server handler interfaces.
// Each method converts types and delegates to the existing Handle* functions.
type marteHandler struct{}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

func (h *marteHandler) Initialize(ctx context.Context, params *golsp.InitializeParams) (*golsp.InitializeResult, error) {
	root := ""
	if params.RootURI != nil && *params.RootURI != "" {
		root = uriToPath(string(*params.RootURI))
	} else if params.RootPath != nil && *params.RootPath != "" {
		root = *params.RootPath
	}

	if root != "" {
		view := GlobalSession.CreateView("main", root)
		snap := view.Snapshot()
		logger.Printf("Scanning workspace: %s\n", root)
		if err := snap.Tree().ScanDirectory(root); err != nil {
			logger.Printf("ScanDirectory failed: %v\n", err)
		}
		snap.Tree().ResolveReferences(nil)
		snap.Tree().ResolveFields(nil)
		view.SetSnapshot(snap)
		GlobalSchema = schema.LoadFullSchema(root)
		logger.Printf("Workspace ready\n")

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
	return nil
}

// ─── Client reference (for publishing diagnostics) ────────────────────────────

func (h *marteHandler) SetClient(client *golspserver.Client) {
	// Wire the go-lsp client into the diagnostic publishing path so that
	// runValidation / publishImmediateDiagnostics use the proper channel
	// instead of writing raw JSON-RPC to stdout.
	PublishDiagnosticsFn = func(ctx context.Context, fileURI string, diags []LSPDiagnostic) {
		golspDiags := make([]golsp.Diagnostic, len(diags))
		for i, d := range diags {
			sev := golsp.DiagnosticSeverity(d.Severity)
			golspDiags[i] = golsp.Diagnostic{
				Range: golsp.Range{
					Start: golsp.Position{Line: d.Range.Start.Line, Character: d.Range.Start.Character},
					End:   golsp.Position{Line: d.Range.End.Line, Character: d.Range.End.Character},
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
	HandleDidOpen(DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:  string(params.TextDocument.URI),
			Text: params.TextDocument.Text,
		},
	})
	return nil
}

func (h *marteHandler) DidChange(ctx context.Context, params *golsp.DidChangeTextDocumentParams) error {
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
	HandleDidClose(DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
	})
	return nil
}

// ─── Language Features ────────────────────────────────────────────────────────

func (h *marteHandler) Hover(ctx context.Context, params *golsp.HoverParams) (*golsp.Hover, error) {
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
	res := HandleDefinition(DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	return convertLocationsResult(res), nil
}

func (h *marteHandler) TypeDefinition(ctx context.Context, params *golsp.TypeDefinitionParams) ([]golsp.Location, error) {
	res := HandleTypeDefinition(TypeDefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	return convertLocationsResult(res), nil
}

func (h *marteHandler) References(ctx context.Context, params *golsp.ReferenceParams) ([]golsp.Location, error) {
	locs := HandleReferences(ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
		Context:      ReferenceContext{IncludeDeclaration: params.Context.IncludeDeclaration},
	})
	return convertLocations(locs), nil
}

func (h *marteHandler) Completion(ctx context.Context, params *golsp.CompletionParams) (*golsp.CompletionList, error) {
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
	edits := HandleFormatting(DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
	})
	return convertTextEdits(edits), nil
}

func (h *marteHandler) Rename(ctx context.Context, params *golsp.RenameParams) (*golsp.WorkspaceEdit, error) {
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
			Position: golsp.Position{Line: hint.Position.Line, Character: hint.Position.Character},
			Label:    labelJSON,
			Kind:     &kind,
		}
	}
	return result, nil
}

func (h *marteHandler) DocumentSymbol(ctx context.Context, params *golsp.DocumentSymbolParams) ([]golsp.DocumentSymbol, error) {
	syms := HandleDocumentSymbol(DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
	})
	return convertDocumentSymbols(syms), nil
}

func (h *marteHandler) WorkspaceSymbol(ctx context.Context, params *golsp.WorkspaceSymbolParams) ([]golsp.SymbolInformation, error) {
	syms := HandleWorkspaceSymbol(WorkspaceSymbolParams{Query: params.Query})
	result := make([]golsp.SymbolInformation, len(syms))
	for i, s := range syms {
		result[i] = golsp.SymbolInformation{
			Name: s.Name,
			Kind: golsp.SymbolKind(s.Kind),
			Location: golsp.Location{
				URI: golsp.DocumentURI(s.Location.URI),
				Range: golsp.Range{
					Start: golsp.Position{Line: s.Location.Range.Start.Line, Character: s.Location.Range.Start.Character},
					End:   golsp.Position{Line: s.Location.Range.End.Line, Character: s.Location.Range.End.Character},
				},
			},
			ContainerName: s.ContainerName,
		}
	}
	return result, nil
}

func (h *marteHandler) CodeAction(ctx context.Context, params *golsp.CodeActionParams) ([]golsp.CodeAction, error) {
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
	items := HandlePrepareCallHierarchy(CallHierarchyPrepareParams{
		TextDocument: TextDocumentIdentifier{URI: string(params.TextDocument.URI)},
		Position:     Position{Line: params.Position.Line, Character: params.Position.Character},
	})
	return convertCallHierarchyItemsTo(items), nil
}

func (h *marteHandler) IncomingCalls(ctx context.Context, params *golsp.CallHierarchyIncomingCallsParams) ([]golsp.CallHierarchyIncomingCall, error) {
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
	_ golspserver.LifecycleHandler       = (*marteHandler)(nil)
	_ golspserver.ClientHandler          = (*marteHandler)(nil)
	_ golspserver.TextDocumentSyncHandler = (*marteHandler)(nil)
	_ golspserver.HoverHandler           = (*marteHandler)(nil)
	_ golspserver.DefinitionHandler      = (*marteHandler)(nil)
	_ golspserver.TypeDefinitionHandler  = (*marteHandler)(nil)
	_ golspserver.ReferencesHandler      = (*marteHandler)(nil)
	_ golspserver.CompletionHandler      = (*marteHandler)(nil)
	_ golspserver.DocumentFormattingHandler = (*marteHandler)(nil)
	_ golspserver.RenameHandler          = (*marteHandler)(nil)
	_ golspserver.InlayHintHandler       = (*marteHandler)(nil)
	_ golspserver.DocumentSymbolHandler  = (*marteHandler)(nil)
	_ golspserver.WorkspaceSymbolHandler = (*marteHandler)(nil)
	_ golspserver.CodeActionHandler      = (*marteHandler)(nil)
	_ golspserver.CallHierarchyHandler   = (*marteHandler)(nil)
)

// ─── Type conversion helpers ─────────────────────────────────────────────────

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
				Start: golsp.Position{Line: l.Range.Start.Line, Character: l.Range.Start.Character},
				End:   golsp.Position{Line: l.Range.End.Line, Character: l.Range.End.Character},
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
				Start: golsp.Position{Line: e.Range.Start.Line, Character: e.Range.Start.Character},
				End:   golsp.Position{Line: e.Range.End.Line, Character: e.Range.End.Character},
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
				Start: golsp.Position{Line: s.Range.Start.Line, Character: s.Range.Start.Character},
				End:   golsp.Position{Line: s.Range.End.Line, Character: s.Range.End.Character},
			},
			SelectionRange: golsp.Range{
				Start: golsp.Position{Line: s.SelectionRange.Start.Line, Character: s.SelectionRange.Start.Character},
				End:   golsp.Position{Line: s.SelectionRange.End.Line, Character: s.SelectionRange.End.Character},
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
			Start: golsp.Position{Line: item.Range.Start.Line, Character: item.Range.Start.Character},
			End:   golsp.Position{Line: item.Range.End.Line, Character: item.Range.End.Character},
		},
		SelectionRange: golsp.Range{
			Start: golsp.Position{Line: item.SelectionRange.Start.Line, Character: item.SelectionRange.Start.Character},
			End:   golsp.Position{Line: item.SelectionRange.End.Line, Character: item.SelectionRange.End.Character},
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
			Start: golsp.Position{Line: r.Start.Line, Character: r.Start.Character},
			End:   golsp.Position{Line: r.End.Line, Character: r.End.Character},
		}
	}
	return result
}
