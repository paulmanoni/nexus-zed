// Package decorators is the heart of the nexus Zed extension: it detects the
// `//@` handler-registration annotations nexus understands (see the framework's
// decorator-form registration) and turns a Go source file into editor-friendly
// facts — diagnostics, an outline of decorated handlers, hover docs, and
// completion items. It is transport-neutral: positions are plain 0-based
// line/character pairs so the LSP layer can map them to protocol types and the
// logic stays unit-testable without any LSP machinery.
package decorators

import (
	"fmt"
	"sort"
	"strings"
)

// Severity mirrors the LSP DiagnosticSeverity scale.
const (
	SevError   = 1
	SevWarning = 2
	SevInfo    = 3
	SevHint    = 4
)

// Kind classifies a decorator.
type Kind int

const (
	// Primary decorators register an endpoint/provider — at most one per func.
	Primary Kind = iota
	// Modifier decorators tweak a primary (auth, middleware) — need a primary.
	Modifier
)

// Spec describes one known decorator keyword.
type Spec struct {
	Name    string
	Kind    Kind
	MinArgs int
	MaxArgs int // -1 = unbounded
	Usage   string
	Doc     string
}

// Catalog is the set of built-in nexus decorators, keyed by keyword.
var Catalog = map[string]Spec{
	"provide": {"provide", Primary, 0, 0, "//@provide",
		"Register the function as a DI constructor (nexus.Provide). Its return value is injected wherever the type is requested."},
	"rest": {"rest", Primary, 2, 2, "//@rest <METHOD> <PATH>",
		"Mount the handler as a REST route (nexus.AsRest), e.g. `//@rest GET /users/:id`. Path params bind via `uri:\"name\"` tags."},
	"query": {"query", Primary, 0, 0, "//@query",
		"Expose the handler as a GraphQL query (nexus.AsQuery). Field name = constructor name minus `New`, lowercased."},
	"mutation": {"mutation", Primary, 0, 0, "//@mutation",
		"Expose the handler as a GraphQL mutation (nexus.AsMutation)."},
	"subscription": {"subscription", Primary, 0, 0, "//@subscription",
		"Expose the handler as a GraphQL subscription (nexus.AsSubscription)."},
	"ws": {"ws", Primary, 2, 2, "//@ws <PATH> <TYPE>",
		"Register a WebSocket message handler (nexus.AsWS), e.g. `//@ws /events chat.send`. Handlers on one path share a connection, dispatched by envelope type."},
	"worker": {"worker", Primary, 1, 1, "//@worker <NAME>",
		"Register a background worker (nexus.AsWorker). First param must be context.Context; the rest are DI-injected."},
	"auth": {"auth", Modifier, 1, -1, "//@auth Required | Requires(\"ROLE\")",
		"Add an auth gate to the primary decorator: `//@auth Required` (401 if missing) or `//@auth Requires(\"ROLE_X\")` (403)."},
	"use": {"use", Modifier, 1, -1, "//@use <expr>",
		"Attach per-op middleware to the primary decorator, e.g. `//@use ratelimit.PerUser(100, time.Minute)`. The expression is resolved from the file's imports."},
}

// Annotation is a single `//@...` comment line.
type Annotation struct {
	Keyword   string   // e.g. "rest" or, for a custom decorator, "inertia.Page"
	Args      []string // whitespace-separated tokens after the keyword
	Line      int      // 0-based line
	KwStart   int      // 0-based char where the keyword starts (after //@)
	KwEnd     int      // 0-based char just past the keyword
	Qualified bool     // true if keyword contains a dot (custom extension decorator)
}

// Spec returns the catalog entry for a built-in keyword (ok=false otherwise).
func (a Annotation) Spec() (Spec, bool) { s, ok := Catalog[a.Keyword]; return s, ok }

// Block is a run of annotation lines and the function declaration they attach to.
type Block struct {
	Annotations []Annotation
	FuncName    string // "" if the block is not attached to a func
	FuncLine    int    // 0-based line of the func decl (-1 if unattached)
}

// Primary returns the block's primary annotations (built-in primaries + any
// qualified custom decorator, which always acts as a primary).
func (b Block) Primary() []Annotation {
	var out []Annotation
	for _, a := range b.Annotations {
		if a.Qualified {
			out = append(out, a)
			continue
		}
		if s, ok := a.Spec(); ok && s.Kind == Primary {
			out = append(out, a)
		}
	}
	return out
}

// Pos is a 0-based line/character position.
type Pos struct{ Line, Char int }

// Range is a half-open [Start,End) span on a single or multiple lines.
type Range struct{ Start, End Pos }

// Diagnostic is one problem found in a document.
type Diagnostic struct {
	Range    Range
	Severity int
	Message  string
	Code     string
}

// Symbol is one outline entry (a decorated function).
type Symbol struct {
	Name           string
	Detail         string
	Range          Range
	SelectionRange Range
}

// Parse scans source text and returns the decorator blocks it contains.
// It is deliberately tolerant of in-progress / invalid Go (it never calls
// go/parser): it works line-by-line so the editor gets results mid-edit.
//
// It mirrors how nexus's scanner (deco) associates directives: a directive is
// "attached" only when it sits in a contiguous comment group that is
// immediately followed by a func declaration — exactly go/ast doc-group
// semantics, where a blank line between the comment and the func breaks the
// association.
func Parse(text string) []Block {
	lines := splitLines(text)
	var blocks []Block
	i := 0
	for i < len(lines) {
		if !isCommentLine(lines[i]) {
			i++
			continue
		}
		// Consume a contiguous comment group [start, i).
		start := i
		for i < len(lines) && isCommentLine(lines[i]) {
			i++
		}
		var anns []Annotation
		for ln := start; ln < i; ln++ {
			if a, ok := parseAnnotation(lines[ln], ln); ok {
				anns = append(anns, a)
			}
		}
		if len(anns) == 0 {
			continue
		}
		blk := Block{Annotations: anns, FuncLine: -1}
		// The line right after the group (no blank line in between, or it
		// wouldn't be the immediate successor) is the doc target.
		if i < len(lines) {
			if name, ok := funcName(strings.TrimSpace(lines[i])); ok {
				blk.FuncName = name
				blk.FuncLine = i
			}
		}
		blocks = append(blocks, blk)
	}
	return blocks
}

// isCommentLine reports whether a line is a // line comment (any indentation).
func isCommentLine(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "//")
}

// httpMethods is the set of HTTP verbs //@rest / nexus routers expect.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

// Diagnostics validates every block and returns the problems found.
//
// Severity policy mirrors nexus's own contract. An Error is anything
// `nexus generate handlers` HARD-REJECTS — the handler silently won't register,
// so it must be loud: unknown keyword, wrong argument count, a bad //@auth
// value, a modifier with no primary, more than one primary, and a modifier on a
// custom //@pkg.Func. A Warning is something nexus still accepts but is almost
// certainly a mistake (a non-standard HTTP method, a path missing its leading
// slash, a decorator not attached to a func — which nexus simply ignores).
func Diagnostics(text string) []Diagnostic {
	var out []Diagnostic
	for _, blk := range Parse(text) {
		primaries := 0
		hasPrimary := false
		hasQualified := false
		for _, a := range blk.Annotations {
			rng := Range{Pos{a.Line, a.KwStart}, Pos{a.Line, a.KwEnd}}
			if a.Qualified {
				// Custom extension decorator (//@pkg.Func …) — counts as primary.
				primaries++
				hasPrimary = true
				hasQualified = true
				continue
			}
			spec, known := a.Spec()
			if !known {
				msg := fmt.Sprintf("unknown nexus decorator %q — `nexus generate handlers` will fail", a.Keyword)
				if sug := suggest(a.Keyword); sug != "" {
					msg += fmt.Sprintf("; did you mean //@%s?", sug)
				}
				out = append(out, Diagnostic{rng, SevError, msg, "unknown-decorator"})
				continue
			}
			if spec.Kind == Primary {
				primaries++
				hasPrimary = true
			}
			if n := len(a.Args); n < spec.MinArgs || (spec.MaxArgs >= 0 && n > spec.MaxArgs) {
				out = append(out, Diagnostic{rng, SevError,
					fmt.Sprintf("//@%s takes %s, got %d — usage: %s", a.Keyword, argCount(spec), n, spec.Usage),
					"bad-args"})
				continue // arg-value checks below are meaningless with a wrong count
			}
			switch a.Keyword {
			case "auth":
				if !validAuth(a.Args) {
					out = append(out, Diagnostic{rng, SevError,
						`//@auth must be Required or Requires("ROLE") — e.g. //@auth Requires("ADMIN")`, "bad-auth"})
				}
			case "rest":
				out = append(out, restArgChecks(a, rng)...)
			case "ws":
				if !strings.HasPrefix(a.Args[0], "/") {
					out = append(out, Diagnostic{rng, SevWarning,
						fmt.Sprintf("//@ws path %q should start with '/'", a.Args[0]), "bad-path"})
				}
			}
		}
		if primaries > 1 {
			a := blk.Annotations[0]
			out = append(out, Diagnostic{
				Range{Pos{a.Line, a.KwStart}, Pos{a.Line, a.KwEnd}}, SevError,
				"multiple primary decorators on one function — nexus allows only one per handler", "multiple-primary"})
		}
		if !hasPrimary && hasModifier(blk) {
			a := blk.Annotations[0]
			out = append(out, Diagnostic{
				Range{Pos{a.Line, a.KwStart}, Pos{a.Line, a.KwEnd}}, SevError,
				"modifier decorator (//@auth, //@use) needs a primary like //@rest or //@query", "orphan-modifier"})
		}
		if hasQualified && hasModifier(blk) {
			if m := firstModifier(blk); m != nil {
				out = append(out, Diagnostic{
					Range{Pos{m.Line, m.KwStart}, Pos{m.Line, m.KwEnd}}, SevError,
					"a custom //@pkg.Func decorator does not accept //@auth or //@use modifiers", "custom-no-modifier"})
			}
		}
		if blk.FuncName == "" {
			a := blk.Annotations[0]
			out = append(out, Diagnostic{
				Range{Pos{a.Line, a.KwStart}, Pos{a.Line, a.KwEnd}}, SevWarning,
				"decorator is not attached to a function declaration — nexus will ignore it", "unattached"})
		}
	}
	return out
}

// restArgChecks validates a well-formed //@rest's METHOD and PATH (called only
// when the argument count is already correct).
func restArgChecks(a Annotation, rng Range) []Diagnostic {
	var out []Diagnostic
	method, path := a.Args[0], a.Args[1]
	switch {
	case !httpMethods[strings.ToUpper(method)]:
		out = append(out, Diagnostic{rng, SevWarning,
			fmt.Sprintf("//@rest method %q is not a standard HTTP method (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS)", method),
			"bad-method"})
	case method != strings.ToUpper(method):
		out = append(out, Diagnostic{rng, SevWarning,
			fmt.Sprintf("//@rest method %q should be upper-case: %s", method, strings.ToUpper(method)), "method-case"})
	}
	if !strings.HasPrefix(path, "/") {
		out = append(out, Diagnostic{rng, SevWarning,
			fmt.Sprintf("//@rest path %q should start with '/'", path), "bad-path"})
	}
	return out
}

// firstModifier returns the first built-in modifier annotation in the block.
func firstModifier(blk Block) *Annotation {
	for i := range blk.Annotations {
		a := blk.Annotations[i]
		if a.Qualified {
			continue
		}
		if s, ok := a.Spec(); ok && s.Kind == Modifier {
			return &blk.Annotations[i]
		}
	}
	return nil
}

// Symbols returns one outline entry per decorated function.
func Symbols(text string) []Symbol {
	var out []Symbol
	for _, blk := range Parse(text) {
		if blk.FuncName == "" {
			continue
		}
		out = append(out, Symbol{
			Name:           blk.FuncName,
			Detail:         detail(blk),
			Range:          Range{Pos{blk.Annotations[0].Line, 0}, Pos{blk.FuncLine, 0}},
			SelectionRange: Range{Pos{blk.FuncLine, 0}, Pos{blk.FuncLine, 0}},
		})
	}
	return out
}

// HoverAt returns markdown documentation if pos sits on a `//@keyword`, else "".
func HoverAt(text string, pos Pos) string {
	lines := splitLines(text)
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	a, ok := parseAnnotation(lines[pos.Line], pos.Line)
	if !ok || pos.Char < a.KwStart || pos.Char > a.KwEnd {
		return ""
	}
	if a.Qualified {
		return fmt.Sprintf("**`//@%s`** — custom extension decorator\n\nEmits `%s(args…, fn)`; the registrar returns a `nexus.Option`. Import resolved from this file.", a.Keyword, a.Keyword)
	}
	if s, ok := a.Spec(); ok {
		return fmt.Sprintf("**`%s`**\n\n%s", s.Usage, s.Doc)
	}
	return ""
}

// Semantic token type indices. They line up with the legend the LSP layer
// advertises (["keyword", "constant", "string"]) — all standard Zed syntax
// styles, so every theme colors them. The editor paints only these ranges; the
// rest of the comment stays comment-colored.
const (
	TokKeyword  = 0 // the decorator itself, e.g. `@rest` (the `@` is included)
	TokMethod   = 1 // HTTP method / auth verb — colored like a constant
	TokString   = 2 // path / name / type arguments — colored like a string
	TokFunction = 3 // the Func part of a custom //@pkg.Func decorator
)

// Token is one highlightable span (0-based line/char, byte length).
type Token struct {
	Line, Char, Length, Type int
}

// SemanticTokens returns the highlight spans for every decorator in the text,
// in document order. The LSP layer encodes them into the relative form the
// protocol wants. Coloring a decorator means: the `@keyword` reads as a
// keyword, the HTTP method as a constant, and path/name/type args as strings —
// so `//@rest GET /users/:id` lights up instead of being flat comment grey.
func SemanticTokens(text string) []Token {
	lines := splitLines(text)
	var toks []Token
	for _, blk := range Parse(text) {
		for _, a := range blk.Annotations {
			at := a.KwStart - 1 // column of the leading '@'
			// For a custom //@pkg.Func decorator, split the keyword at the dot:
			// `@pkg` as a keyword and `Func` as a function. This is not just
			// cosmetic — a single semantic token must NOT span the '.', because
			// some editors (Zed) won't colorize a token that crosses it, which
			// is why a dotted decorator would otherwise show up uncolored.
			if dot := strings.LastIndexByte(a.Keyword, '.'); dot >= 0 {
				dotCol := a.KwStart + dot
				toks = append(toks, Token{a.Line, at, dotCol - at, TokKeyword})       // @pkg
				toks = append(toks, Token{a.Line, dotCol + 1, a.KwEnd - dotCol - 1, TokFunction}) // Func
			} else {
				toks = append(toks, Token{a.Line, at, a.KwEnd - a.KwStart + 1, TokKeyword})
			}
			for i, sp := range wordSpans(lines[a.Line], a.KwEnd) {
				if typ := argType(a, i); typ >= 0 {
					toks = append(toks, Token{a.Line, sp.start, sp.length, typ})
				}
			}
		}
	}
	return toks
}

type span struct{ start, length int }

// wordSpans returns the [start,length) of each whitespace-separated run in line
// at or after byte index from.
func wordSpans(line string, from int) []span {
	var out []span
	i := from
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		out = append(out, span{start, i - start})
	}
	return out
}

// argType picks the token type for the i-th argument of a decorator, or -1 to
// leave it uncolored (e.g. //@use, whose argument is an arbitrary Go expression
// we don't try to tokenize).
func argType(a Annotation, i int) int {
	if a.Qualified {
		return TokString // custom //@pkg.Func args are typically quoted strings
	}
	switch a.Keyword {
	case "rest":
		if i == 0 {
			return TokMethod // METHOD
		}
		return TokString // PATH
	case "ws", "worker":
		return TokString // PATH/TYPE, NAME
	case "auth":
		if i == 0 {
			return TokMethod // Required / Requires(...)
		}
		return -1
	default:
		return -1
	}
}

// CompletionItem is a single completion suggestion.
type CompletionItem struct {
	Label  string
	Detail string
	Doc    string
}

// Completions returns the catalog as completion items, ordered primaries-first.
// The LSP layer decides when to offer them (after `//@`).
func Completions() []CompletionItem {
	keys := make([]string, 0, len(Catalog))
	for k := range Catalog {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ki, kj := Catalog[keys[i]], Catalog[keys[j]]
		if ki.Kind != kj.Kind {
			return ki.Kind < kj.Kind // primaries (0) before modifiers (1)
		}
		return keys[i] < keys[j]
	})
	out := make([]CompletionItem, 0, len(keys))
	for _, k := range keys {
		s := Catalog[k]
		out = append(out, CompletionItem{Label: k, Detail: s.Usage, Doc: s.Doc})
	}
	return out
}

// --- internal helpers ---

func splitLines(text string) []string {
	// Normalize CRLF so column math stays correct; keep no trailing empty.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(text, "\n")
}

// parseAnnotation recognizes a decorator comment line. It matches what nexus's
// scanner accepts: a // line comment whose text, after stripping the slashes
// and any leading space, begins with `@`. This means BOTH the canonical
// `//@rest` and the gofmt-rewritten `// @rest` (gofmt inserts a space after
// `//`) are detected — important, because nexus accepts both. Leading
// indentation is allowed; code with a trailing `//@` note is not (the marker
// must start the line's comment).
func parseAnnotation(line string, lineNo int) (Annotation, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++ // leading indentation
	}
	if i+1 >= len(line) || line[i] != '/' || line[i+1] != '/' {
		return Annotation{}, false
	}
	i += 2
	for i < len(line) && line[i] == '/' {
		i++ // tolerate ///
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++ // gofmt's space: "// @rest"
	}
	if i >= len(line) || line[i] != '@' {
		return Annotation{}, false
	}
	i++ // past '@'
	kwStart := i
	for i < len(line) {
		c := line[i]
		if c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			i++
			continue
		}
		break
	}
	if i == kwStart {
		return Annotation{}, false
	}
	kw := line[kwStart:i]
	return Annotation{
		Keyword:   kw,
		Args:      strings.Fields(line[i:]),
		Line:      lineNo,
		KwStart:   kwStart,
		KwEnd:     i,
		Qualified: strings.Contains(kw, "."),
	}, true
}

// funcName extracts the name from a trimmed line beginning a func declaration,
// handling both plain funcs and methods with a receiver.
func funcName(t string) (string, bool) {
	if !strings.HasPrefix(t, "func") {
		return "", false
	}
	r := strings.TrimSpace(t[len("func"):])
	if r == "" {
		return "", false
	}
	// Skip a receiver: func (r T) Name(...)
	if strings.HasPrefix(r, "(") {
		if c := strings.IndexByte(r, ')'); c >= 0 {
			r = strings.TrimSpace(r[c+1:])
		}
	}
	// Name = leading identifier.
	end := 0
	for end < len(r) {
		c := r[end]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (end > 0 && c >= '0' && c <= '9') {
			end++
			continue
		}
		break
	}
	if end == 0 {
		return "", false
	}
	return r[:end], true
}

func detail(blk Block) string {
	if p := blk.Primary(); len(p) > 0 {
		a := p[0]
		if len(a.Args) > 0 {
			return "//@" + a.Keyword + " " + strings.Join(a.Args, " ")
		}
		return "//@" + a.Keyword
	}
	if len(blk.Annotations) > 0 {
		return "//@" + blk.Annotations[0].Keyword
	}
	return ""
}

func hasModifier(blk Block) bool {
	for _, a := range blk.Annotations {
		if a.Qualified {
			continue
		}
		if s, ok := a.Spec(); ok && s.Kind == Modifier {
			return true
		}
	}
	return false
}

func argCount(s Spec) string {
	switch {
	case s.MinArgs == s.MaxArgs:
		return fmt.Sprintf("%d arg(s)", s.MinArgs)
	case s.MaxArgs < 0:
		return fmt.Sprintf("at least %d arg(s)", s.MinArgs)
	default:
		return fmt.Sprintf("%d–%d args", s.MinArgs, s.MaxArgs)
	}
}

func validAuth(args []string) bool {
	switch {
	case args[0] == "Required":
		return true
	case strings.HasPrefix(args[0], "Requires("):
		return true
	}
	return false
}

// suggest returns the closest catalog keyword within edit distance 2, or "".
func suggest(kw string) string {
	best, bestD := "", 3
	for k := range Catalog {
		if d := levenshtein(kw, k); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
