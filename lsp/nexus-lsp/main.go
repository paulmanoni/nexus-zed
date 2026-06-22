// Command nexus-lsp is a tiny language server for the nexus framework's `//@`
// decorator handlers. It speaks LSP over stdio and powers the nexus Zed
// extension: live diagnostics for malformed/unknown decorators, an outline of
// every decorated handler (documentSymbol), hover docs, and keyword completion
// after `//@`. All real logic lives in the decorators package; this file is the
// JSON-RPC plumbing and LSP glue.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/paulmanoni/nexus-zed/lsp/decorators"
)

// version is overridable at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

type server struct {
	w   *bufio.Writer
	mu  sync.Mutex // guards w (writes) and docs
	wmu sync.Mutex // serializes writes to stdout

	docs map[string]string // uri -> full text
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("nexus-lsp: ")
	log.SetFlags(0)

	s := &server{
		w:    bufio.NewWriter(os.Stdout),
		docs: map[string]string{},
	}
	if err := s.run(bufio.NewReader(os.Stdin)); err != nil && err != io.EOF {
		log.Fatalf("fatal: %v", err)
	}
}

func (s *server) run(r *bufio.Reader) error {
	for {
		msg, err := readMessage(r)
		if err != nil {
			return err
		}
		if s.handle(msg) {
			return nil // exit requested
		}
	}
}

// handle dispatches one message; returns true when the server should exit.
func (s *server) handle(msg *rpcMessage) (exit bool) {
	switch msg.Method {
	case "initialize":
		s.reply(msg.ID, s.capabilities())
	case "initialized":
		// no-op notification
	case "textDocument/didOpen":
		var p didOpenParams
		decode(msg.Params, &p)
		s.setDoc(p.TextDocument.URI, p.TextDocument.Text)
		s.publish(p.TextDocument.URI)
	case "textDocument/didChange":
		var p didChangeParams
		decode(msg.Params, &p)
		if len(p.Changes) > 0 {
			// Full-document sync (see capabilities): last change is the text.
			s.setDoc(p.TextDocument.URI, p.Changes[len(p.Changes)-1].Text)
			s.publish(p.TextDocument.URI)
		}
	case "textDocument/didClose":
		var p didCloseParams
		decode(msg.Params, &p)
		s.delDoc(p.TextDocument.URI)
		s.send(&rpcMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics",
			Params: mustRaw(publishDiagnosticsParams{URI: p.TextDocument.URI, Diagnostics: []diagnostic{}})})
	case "textDocument/documentSymbol":
		var p documentSymbolParams
		decode(msg.Params, &p)
		s.reply(msg.ID, s.symbols(p.TextDocument.URI))
	case "textDocument/hover":
		var p docPositionParams
		decode(msg.Params, &p)
		s.reply(msg.ID, s.hover(p))
	case "textDocument/completion":
		var p docPositionParams
		decode(msg.Params, &p)
		s.reply(msg.ID, s.completion(p))
	case "shutdown":
		s.reply(msg.ID, nil)
	case "exit":
		return true
	default:
		// Unknown request: respond with null so the client isn't left waiting.
		if len(msg.ID) > 0 {
			s.reply(msg.ID, nil)
		}
	}
	return false
}

func (s *server) capabilities() map[string]any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync":       1, // full sync
			"documentSymbolProvider": true,
			"hoverProvider":          true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"@"},
			},
		},
		"serverInfo": map[string]any{"name": "nexus-lsp", "version": version},
	}
}

// --- document store ---

func (s *server) setDoc(uri, text string) {
	s.mu.Lock()
	s.docs[uri] = text
	s.mu.Unlock()
}

func (s *server) getDoc(uri string) (string, bool) {
	s.mu.Lock()
	t, ok := s.docs[uri]
	s.mu.Unlock()
	return t, ok
}

func (s *server) delDoc(uri string) {
	s.mu.Lock()
	delete(s.docs, uri)
	s.mu.Unlock()
}

// --- feature handlers ---

func (s *server) publish(uri string) {
	text, ok := s.getDoc(uri)
	if !ok {
		return
	}
	var diags []diagnostic
	for _, d := range decorators.Diagnostics(text) {
		diags = append(diags, diagnostic{
			Range:    toRange(d.Range),
			Severity: d.Severity,
			Code:     d.Code,
			Source:   "nexus",
			Message:  d.Message,
		})
	}
	if diags == nil {
		diags = []diagnostic{}
	}
	s.send(&rpcMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics",
		Params: mustRaw(publishDiagnosticsParams{URI: uri, Diagnostics: diags})})
}

func (s *server) symbols(uri string) []documentSymbol {
	text, ok := s.getDoc(uri)
	if !ok {
		return []documentSymbol{}
	}
	out := []documentSymbol{}
	for _, sym := range decorators.Symbols(text) {
		out = append(out, documentSymbol{
			Name:           sym.Name,
			Detail:         sym.Detail,
			Kind:           symbolFunction,
			Range:          toRange(sym.Range),
			SelectionRange: toRange(sym.SelectionRange),
		})
	}
	return out
}

func (s *server) hover(p docPositionParams) any {
	text, ok := s.getDoc(p.TextDocument.URI)
	if !ok {
		return nil
	}
	md := decorators.HoverAt(text, decorators.Pos{Line: p.Position.Line, Char: p.Position.Character})
	if md == "" {
		return nil
	}
	return hover{Contents: markupContent{Kind: "markdown", Value: md}}
}

func (s *server) completion(p docPositionParams) []completionItem {
	text, ok := s.getDoc(p.TextDocument.URI)
	if !ok {
		return nil
	}
	// Only offer decorator completions when the cursor sits in a `//@…` prefix.
	if !inDecoratorPrefix(text, p.Position) {
		return nil
	}
	out := []completionItem{}
	for _, it := range decorators.Completions() {
		ci := completionItem{
			Label:      it.Label,
			Kind:       completionKeyword,
			Detail:     it.Detail,
			InsertText: it.Label,
		}
		if it.Doc != "" {
			ci.Documentation = &markupContent{Kind: "markdown", Value: it.Doc}
		}
		out = append(out, ci)
	}
	return out
}

// inDecoratorPrefix reports whether the text on the cursor's line, up to the
// cursor, is a comment marker (`//@` or the gofmt'd `// @`) followed by an
// optional partial keyword — i.e. the spot to offer decorator completions.
func inDecoratorPrefix(text string, pos position) bool {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return false
	}
	line := lines[pos.Line]
	if pos.Character > len(line) {
		pos.Character = len(line)
	}
	prefix := line[:pos.Character]
	i := 0
	for i < len(prefix) && (prefix[i] == ' ' || prefix[i] == '\t') {
		i++
	}
	if i+1 >= len(prefix) || prefix[i] != '/' || prefix[i+1] != '/' {
		return false
	}
	i += 2
	for i < len(prefix) && prefix[i] == '/' {
		i++
	}
	for i < len(prefix) && (prefix[i] == ' ' || prefix[i] == '\t') {
		i++
	}
	if i >= len(prefix) || prefix[i] != '@' {
		return false
	}
	// Everything after @ must be a bare keyword fragment (no spaces yet).
	return !strings.ContainsAny(prefix[i+1:], " \t")
}

func toRange(r decorators.Range) rng {
	return rng{
		Start: position{Line: r.Start.Line, Character: r.Start.Char},
		End:   position{Line: r.End.Line, Character: r.End.Char},
	}
}

// --- JSON-RPC framing ---

func (s *server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // a notification has no id; nothing to reply to
	}
	s.send(&rpcMessage{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) send(msg *rpcMessage) {
	body, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal: %v", err)
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	fmt.Fprintf(s.w, "Content-Length: %d\r\n\r\n", len(body))
	s.w.Write(body)
	s.w.Flush()
}

func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if name, val, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length: %w", err)
			}
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(buf, &msg); err != nil {
		return nil, fmt.Errorf("bad message body: %w", err)
	}
	return &msg, nil
}

func decode(raw json.RawMessage, v any) {
	if len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, v); err != nil {
		log.Printf("decode %T: %v", v, err)
	}
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		log.Printf("marshal params: %v", err)
		return json.RawMessage("null")
	}
	return b
}
