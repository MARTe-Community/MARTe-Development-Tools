package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/marte-dev/marte-dev-tools/internal/index"
	"github.com/marte-dev/marte-dev-tools/internal/parser"
)

type JsonRpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
}

type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Hover struct {
	Contents any `json:"contents"`
}

type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

var tree = index.NewProjectTree()

func RunServer() {
	reader := bufio.NewReader(os.Stdin)
	for {
		msg, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading message: %v\n", err)
			continue
		}

		handleMessage(msg)
	}
}

func readMessage(reader *bufio.Reader) (*JsonRpcMessage, error) {
	var contentLength int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if line == "\r\n" {
			break
		}
		if _, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength); err == nil {
			continue
		}
	}

	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	if err != nil {
		return nil, err
	}

	var msg JsonRpcMessage
	err = json.Unmarshal(body, &msg)
	return &msg, err
}

func handleMessage(msg *JsonRpcMessage) {
	switch msg.Method {
	case "initialize":
		respond(msg.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":   1, // Full sync
				"hoverProvider":      true,
				"definitionProvider": true,
				"referencesProvider": true,
			},
		})
	case "initialized":
		// Do nothing
	case "shutdown":
		respond(msg.ID, nil)
	case "exit":
		os.Exit(0)
	case "textDocument/didOpen":
		var params DidOpenTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			handleDidOpen(params)
		}
	case "textDocument/didChange":
		var params DidChangeTextDocumentParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			handleDidChange(params)
		}
	case "textDocument/hover":
		var params HoverParams
		if err := json.Unmarshal(msg.Params, &params); err == nil {
			fmt.Fprintf(os.Stderr, "Hover: %s:%d\n", params.TextDocument.URI, params.Position.Line)
			res := handleHover(params)
			if res != nil {
				fmt.Fprintf(os.Stderr, "Res: %v\n", res.Contents)
			} else {
				fmt.Fprint(os.Stderr, "Res: NIL\n")
			}
			respond(msg.ID, res)
		} else {
			fmt.Fprint(os.Stderr, "not recovered hover parameters\n")
			respond(msg.ID, nil)
		}
	}
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func handleDidOpen(params DidOpenTextDocumentParams) {
	path := uriToPath(params.TextDocument.URI)
	p := parser.NewParser(params.TextDocument.Text)
	config, err := p.Parse()
	if err == nil {
		tree.AddFile(path, config)
		tree.ResolveReferences()
	}
}

func handleDidChange(params DidChangeTextDocumentParams) {
	if len(params.ContentChanges) == 0 {
		return
	}
	text := params.ContentChanges[0].Text
	path := uriToPath(params.TextDocument.URI)
	p := parser.NewParser(text)
	config, err := p.Parse()
	if err == nil {
		tree.AddFile(path, config)
		tree.ResolveReferences()
	}
}

func handleHover(params HoverParams) *Hover {
	path := uriToPath(params.TextDocument.URI)
	line := params.Position.Line + 1
	col := params.Position.Character + 1

	res := tree.Query(path, line, col)
	if res == nil {
		fmt.Fprint(os.Stderr, "No object/node/reference found\n")
		return nil
	}

	var content string

	if res.Node != nil {
		content = formatNodeInfo(res.Node)
	} else if res.Field != nil {
		content = fmt.Sprintf("**Field**: `%s`", res.Field.Name)
	} else if res.Reference != nil {
		targetName := "Unresolved"
		fullInfo := ""
		targetDoc := ""

		if res.Reference.Target != nil {
			targetName = res.Reference.Target.RealName
			targetDoc = res.Reference.Target.Doc
			fullInfo = formatNodeInfo(res.Reference.Target)
		}

		content = fmt.Sprintf("**Reference**: `%s` -> `%s`", res.Reference.Name, targetName)
		if fullInfo != "" {
			content += fmt.Sprintf("\n\n---\n%s", fullInfo)
		} else if targetDoc != "" { // Fallback if formatNodeInfo returned empty (unlikely)
			content += fmt.Sprintf("\n\n%s", targetDoc)
		}
	}

	if content == "" {
		return nil
	}

	return &Hover{
		Contents: MarkupContent{
			Kind:  "markdown",
			Value: content,
		},
	}
}

func formatNodeInfo(node *index.ProjectNode) string {
	class := node.Metadata["Class"]
	if class == "" {
		class = "Unknown"
	}

	info := fmt.Sprintf("**Object**: `%s`\n\n**Class**: `%s`", node.RealName, class)

	// Check if it's a Signal (has Type or DataSource)
	typ := node.Metadata["Type"]
	ds := node.Metadata["DataSource"]

	if typ != "" || ds != "" {
		sigInfo := "\n"
		if typ != "" {
			sigInfo += fmt.Sprintf("**Type**: `%s` ", typ)
		}
		if ds != "" {
			sigInfo += fmt.Sprintf("**DataSource**: `%s` ", ds)
		}

		// Size
		dims := node.Metadata["NumberOfDimensions"]
		elems := node.Metadata["NumberOfElements"]
		if dims != "" || elems != "" {
			sigInfo += fmt.Sprintf("**Size**: `[%s]`, `%s` dims ", elems, dims)
		}
		info += sigInfo
	}

	if node.Doc != "" {
		info += fmt.Sprintf("\n\n%s", node.Doc)
	}
	return info
}

func respond(id any, result any) {
	msg := JsonRpcMessage{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  result,
	}
	send(msg)
}

func send(msg any) {
	body, _ := json.Marshal(msg)
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
