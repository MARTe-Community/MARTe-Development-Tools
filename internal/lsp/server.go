package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type JsonRpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
}

type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

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
	// LSP uses Content-Length header
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
		respond(msg.ID, map[string]interface{}{
			"capabilities": map[string]interface{}{
				"textDocumentSync": 1, // Full sync
				"hoverProvider":    true,
				"definitionProvider": true,
				"referencesProvider": true,
				"completionProvider": map[string]interface{}{
					"triggerCharacters": []string{"=", ".", "{", "+", "$"},
				},
			},
		})
	case "initialized":
		// Do nothing
	case "shutdown":
		respond(msg.ID, nil)
	case "exit":
		os.Exit(0)
	case "textDocument/didOpen":
		// Handle file open
	case "textDocument/didChange":
		// Handle file change
	case "textDocument/hover":
		// Handle hover
	}
}

func respond(id interface{}, result interface{}) {
	msg := JsonRpcMessage{
		Jsonrpc: "2.0",
		ID:      id,
		Result:  result,
	}
	send(msg)
}

func send(msg interface{}) {
	body, _ := json.Marshal(msg)
	fmt.Printf("Content-Length: %d\r\n\r\n%s", len(body), body)
}
