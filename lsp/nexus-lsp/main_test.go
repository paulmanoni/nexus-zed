package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// newTestServer wires a server whose output goes to an in-memory buffer.
func newTestServer() (*server, *bytes.Buffer) {
	var out bytes.Buffer
	s := &server{w: bufio.NewWriter(&out), docs: map[string]string{}}
	return s, &out
}

func req(method string, id int, params any) *rpcMessage {
	raw, _ := json.Marshal(params)
	idRaw, _ := json.Marshal(id)
	return &rpcMessage{JSONRPC: "2.0", ID: idRaw, Method: method, Params: raw}
}

func note(method string, params any) *rpcMessage {
	raw, _ := json.Marshal(params)
	return &rpcMessage{JSONRPC: "2.0", Method: method, Params: raw}
}

// frames decodes all Content-Length-framed messages written to the buffer.
func frames(t *testing.T, buf *bytes.Buffer) []rpcMessage {
	t.Helper()
	r := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	var msgs []rpcMessage
	for {
		m, err := readMessage(r)
		if err != nil {
			break
		}
		msgs = append(msgs, *m)
	}
	return msgs
}

func TestInitializeAdvertisesCapabilities(t *testing.T) {
	s, out := newTestServer()
	s.handle(req("initialize", 1, map[string]any{}))
	msgs := frames(t, out)
	if len(msgs) != 1 {
		t.Fatalf("got %d replies, want 1", len(msgs))
	}
	var res struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	json.Unmarshal(mustResult(t, msgs[0]), &res)
	for _, cap := range []string{"documentSymbolProvider", "hoverProvider", "completionProvider"} {
		if _, ok := res.Capabilities[cap]; !ok {
			t.Errorf("missing capability %q", cap)
		}
	}
}

func TestDidOpenPublishesDiagnostics(t *testing.T) {
	s, out := newTestServer()
	src := "//@rest GET\nfunc NewX() {}\n" // missing PATH -> bad-args error
	s.handle(note("textDocument/didOpen", didOpenParams{
		TextDocument: versionedTextDocumentItem{URI: "file:///x.go", Text: src},
	}))
	var found bool
	for _, m := range frames(t, out) {
		if m.Method != "textDocument/publishDiagnostics" {
			continue
		}
		var p publishDiagnosticsParams
		json.Unmarshal(mustParams(t, m), &p)
		for _, d := range p.Diagnostics {
			if d.Code == "bad-args" && d.Source == "nexus" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected a bad-args diagnostic from didOpen")
	}
}

func TestDocumentSymbolAfterOpen(t *testing.T) {
	s, out := newTestServer()
	src := "//@query\nfunc NewSearch() {}\n"
	s.handle(note("textDocument/didOpen", didOpenParams{
		TextDocument: versionedTextDocumentItem{URI: "file:///s.go", Text: src},
	}))
	out.Reset()
	s.handle(req("textDocument/documentSymbol", 2, documentSymbolParams{
		TextDocument: textDocumentIdentifier{URI: "file:///s.go"},
	}))
	var syms []documentSymbol
	json.Unmarshal(mustResult(t, frames(t, out)[0]), &syms)
	if len(syms) != 1 || syms[0].Name != "NewSearch" || !strings.Contains(syms[0].Detail, "query") {
		t.Fatalf("unexpected symbols: %+v", syms)
	}
}

func TestCompletionOnlyInDecoratorPrefix(t *testing.T) {
	s, out := newTestServer()
	src := "//@\nfunc NewX() {}\n"
	s.handle(note("textDocument/didOpen", didOpenParams{
		TextDocument: versionedTextDocumentItem{URI: "file:///c.go", Text: src},
	}))
	out.Reset()
	// Cursor right after "//@" on line 0 -> offer the catalog.
	s.handle(req("textDocument/completion", 3, docPositionParams{
		TextDocument: textDocumentIdentifier{URI: "file:///c.go"},
		Position:     position{Line: 0, Character: 3},
	}))
	var items []completionItem
	json.Unmarshal(mustResult(t, frames(t, out)[0]), &items)
	if len(items) == 0 {
		t.Fatal("expected completions after //@")
	}

	// Cursor inside the func body line -> no decorator completions.
	out.Reset()
	s.handle(req("textDocument/completion", 4, docPositionParams{
		TextDocument: textDocumentIdentifier{URI: "file:///c.go"},
		Position:     position{Line: 1, Character: 5},
	}))
	var none []completionItem
	json.Unmarshal(mustResult(t, frames(t, out)[0]), &none)
	if len(none) != 0 {
		t.Fatalf("expected no completions off-decorator, got %d", len(none))
	}
}

func TestShutdownThenExit(t *testing.T) {
	s, _ := newTestServer()
	if s.handle(req("shutdown", 9, nil)) {
		t.Fatal("shutdown should not exit")
	}
	if !s.handle(&rpcMessage{JSONRPC: "2.0", Method: "exit"}) {
		t.Fatal("exit should return true")
	}
}

func mustResult(t *testing.T, m rpcMessage) []byte {
	t.Helper()
	b, err := json.Marshal(m.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return b
}

func mustParams(t *testing.T, m rpcMessage) []byte {
	t.Helper()
	return m.Params
}
