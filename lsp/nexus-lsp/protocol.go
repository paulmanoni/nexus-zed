package main

import "encoding/json"

// Minimal subset of the Language Server Protocol used by nexus-lsp. Only the
// fields we read or write are modeled; everything else is ignored on decode.

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`     // present on requests/responses
	Method  string          `json:"method,omitempty"` // present on requests/notifications
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type rng struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type versionedTextDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type didOpenParams struct {
	TextDocument versionedTextDocumentItem `json:"textDocument"`
}

type contentChange struct {
	Text string `json:"text"` // full-document sync: the whole new text
}

type didChangeParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Changes      []contentChange        `json:"contentChanges"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type docPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

type documentSymbolParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type diagnostic struct {
	Range    rng    `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

// SymbolKind values we use (LSP scale).
const (
	symbolFunction = 12
)

type documentSymbol struct {
	Name           string `json:"name"`
	Detail         string `json:"detail,omitempty"`
	Kind           int    `json:"kind"`
	Range          rng    `json:"range"`
	SelectionRange rng    `json:"selectionRange"`
}

type markupContent struct {
	Kind  string `json:"kind"` // "markdown"
	Value string `json:"value"`
}

type hover struct {
	Contents markupContent `json:"contents"`
}

// CompletionItemKind values we use.
const (
	completionKeyword = 14
)

type completionItem struct {
	Label         string         `json:"label"`
	Kind          int            `json:"kind"`
	Detail        string         `json:"detail,omitempty"`
	Documentation *markupContent `json:"documentation,omitempty"`
	InsertText    string         `json:"insertText,omitempty"`
}
