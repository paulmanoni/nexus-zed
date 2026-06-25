package decorators

import (
	"strings"
	"testing"
)

const sample = `package users

import "github.com/paulmanoni/nexus"

//@provide
func NewService(app *nexus.App) *Service { return &Service{app.Service("users")} }

//@rest GET /users/:id
func NewGetUser(s *Service, p nexus.Params[GetArgs]) (*User, error) { return nil, nil }

//@query
//@auth Requires("ADMIN")
func NewSearchUsers(s *Service, p nexus.Params[SearchArgs]) ([]User, error) { return nil, nil }

//@mutation
func (s *Service) CreateUser(p nexus.Params[NewUser]) (*User, error) { return nil, nil }
`

func TestParse_AttachesToFunc(t *testing.T) {
	blocks := Parse(sample)
	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4", len(blocks))
	}
	want := map[string]string{
		"NewService":     "provide",
		"NewGetUser":     "rest",
		"NewSearchUsers": "query",
		"CreateUser":     "mutation",
	}
	for _, b := range blocks {
		if b.FuncName == "" {
			t.Fatalf("block %+v not attached", b)
		}
		kw := b.Annotations[0].Keyword
		if want[b.FuncName] != kw {
			t.Errorf("func %s: first decorator = %q, want %q", b.FuncName, kw, want[b.FuncName])
		}
	}
}

func TestParse_MethodReceiver(t *testing.T) {
	for _, b := range Parse(sample) {
		if b.Annotations[0].Keyword == "mutation" {
			if b.FuncName != "CreateUser" {
				t.Fatalf("method receiver func name = %q, want CreateUser", b.FuncName)
			}
			return
		}
	}
	t.Fatal("mutation block not found")
}

func TestDiagnostics_Clean(t *testing.T) {
	if d := Diagnostics(sample); len(d) != 0 {
		t.Fatalf("expected no diagnostics, got %d: %+v", len(d), d)
	}
}

func TestDiagnostics_UnknownWithSuggestion(t *testing.T) {
	src := "//@reset GET /x\nfunc NewX() {}\n"
	d := Diagnostics(src)
	if len(d) != 1 || d[0].Code != "unknown-decorator" {
		t.Fatalf("want 1 unknown-decorator diag, got %+v", d)
	}
	if !strings.Contains(d[0].Message, "did you mean //@rest") {
		t.Errorf("missing suggestion: %q", d[0].Message)
	}
}

func TestDiagnostics_HardRejectionsAreErrors(t *testing.T) {
	// Everything nexus generate handlers rejects must surface as an Error.
	cases := []struct {
		name string
		src  string
		code string
	}{
		{"unknown", "//@reset\nfunc NewX() {}\n", "unknown-decorator"},
		{"bad-args", "//@rest GET\nfunc NewX() {}\n", "bad-args"},
		{"bad-auth", "//@query\n//@auth Admin\nfunc NewX() {}\n", "bad-auth"},
		{"orphan-modifier", "//@auth Required\nfunc NewX() {}\n", "orphan-modifier"},
		{"multiple-primary", "//@query\n//@mutation\nfunc NewX() {}\n", "multiple-primary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := findCode(Diagnostics(tc.src), tc.code)
			if d == nil {
				t.Fatalf("missing %s: %+v", tc.code, Diagnostics(tc.src))
			}
			if d.Severity != SevError {
				t.Fatalf("%s severity = %d, want Error(%d)", tc.code, d.Severity, SevError)
			}
		})
	}
}

func TestDiagnostics_CustomDecoratorAcceptsModifiers(t *testing.T) {
	// nexus appends //@auth/@use as trailing options to a custom //@pkg.Func
	// registrar (e.g. inertia.Page(..., auth.Required())), so it must NOT be
	// flagged as a hard rejection.
	src := "//@auth Required\n//@inertia.Page \"GET\" \"/x\" \"Y\"\nfunc NewX() {}\n"
	if d := findCode(Diagnostics(src), "custom-no-modifier"); d != nil {
		t.Fatalf("custom-no-modifier should no longer be emitted: %+v", d)
	}
	for _, d := range Diagnostics(src) {
		if d.Severity == SevError {
			t.Fatalf("unexpected error diagnostic on custom decorator + modifier: %+v", d)
		}
	}
}

func TestDiagnostics_RestMethodAndPathLint(t *testing.T) {
	if d := findCode(Diagnostics("//@rest FETCH /x\nfunc NewX() {}\n"), "bad-method"); d == nil || d.Severity != SevWarning {
		t.Fatalf("expected bad-method warning, got %+v", Diagnostics("//@rest FETCH /x\nfunc NewX() {}\n"))
	}
	if d := findCode(Diagnostics("//@rest get /x\nfunc NewX() {}\n"), "method-case"); d == nil {
		t.Fatalf("expected method-case warning for lowercase verb")
	}
	if d := findCode(Diagnostics("//@rest GET users\nfunc NewX() {}\n"), "bad-path"); d == nil {
		t.Fatalf("expected bad-path warning for path without leading slash")
	}
}

func TestDiagnostics_BadArgs(t *testing.T) {
	src := "//@rest GET\nfunc NewX() {}\n" // missing PATH
	d := Diagnostics(src)
	if len(d) != 1 || d[0].Code != "bad-args" || d[0].Severity != SevError {
		t.Fatalf("want 1 bad-args error, got %+v", d)
	}
}

func TestDiagnostics_OrphanModifier(t *testing.T) {
	src := "//@auth Required\nfunc NewX() {}\n"
	if d := findCode(Diagnostics(src), "orphan-modifier"); d == nil {
		t.Fatalf("expected orphan-modifier, got %+v", Diagnostics(src))
	}
}

func TestDiagnostics_Unattached(t *testing.T) {
	src := "//@query\n\nvar x = 1\n"
	if d := findCode(Diagnostics(src), "unattached"); d == nil {
		t.Fatalf("expected unattached, got %+v", Diagnostics(src))
	}
}

// A blank line between the decorator and the func breaks the doc group, exactly
// like go/ast — so nexus would NOT pick it up, and we flag it as unattached.
func TestParse_BlankLineBreaksAttachment(t *testing.T) {
	src := "//@query\n\nfunc NewX() {}\n"
	blocks := Parse(src)
	if len(blocks) != 1 || blocks[0].FuncName != "" {
		t.Fatalf("blank line should break attachment, got %+v", blocks)
	}
}

// gofmt rewrites //@rest to "// @rest" (a space after the slashes); nexus still
// accepts it, so we must detect both forms identically.
func TestParse_GofmtSpacedForm(t *testing.T) {
	src := "// @rest GET /users/:id\nfunc NewGetUser() {}\n"
	blocks := Parse(src)
	if len(blocks) != 1 || blocks[0].FuncName != "NewGetUser" {
		t.Fatalf("spaced form not attached: %+v", blocks)
	}
	if kw := blocks[0].Annotations[0].Keyword; kw != "rest" {
		t.Fatalf("spaced form keyword = %q, want rest", kw)
	}
	if d := Diagnostics(src); len(d) != 0 {
		t.Fatalf("spaced form should be clean, got %+v", d)
	}
	// Hover must land on the keyword at its real column (after "// @").
	a := blocks[0].Annotations[0]
	if h := HoverAt(src, Pos{Line: 0, Char: a.KwStart}); !strings.Contains(h, "REST route") {
		t.Fatalf("hover on spaced keyword failed (col %d): %q", a.KwStart, h)
	}
}

func TestDiagnostics_MultiplePrimary(t *testing.T) {
	src := "//@query\n//@mutation\nfunc NewX() {}\n"
	if d := findCode(Diagnostics(src), "multiple-primary"); d == nil {
		t.Fatalf("expected multiple-primary, got %+v", Diagnostics(src))
	}
}

func TestDiagnostics_CustomDecoratorIsPrimary(t *testing.T) {
	// A qualified custom decorator counts as a primary, so //@auth alongside
	// it must NOT be flagged as orphaned, and it must not be "unknown".
	src := "//@inertia.Page GET /users Users/Index\nfunc NewUsers() {}\n"
	for _, d := range Diagnostics(src) {
		t.Fatalf("custom decorator should be clean, got: %+v", d)
	}
}

func TestSymbols(t *testing.T) {
	syms := Symbols(sample)
	if len(syms) != 4 {
		t.Fatalf("got %d symbols, want 4", len(syms))
	}
	for _, s := range syms {
		if s.Name == "NewGetUser" && s.Detail != "//@rest GET /users/:id" {
			t.Errorf("NewGetUser detail = %q", s.Detail)
		}
	}
}

func TestHoverAt(t *testing.T) {
	// "//@rest" starts at line 7 (0-based), keyword at char 3.
	src := "//@rest GET /users/:id\nfunc NewX() {}\n"
	if h := HoverAt(src, Pos{Line: 0, Char: 4}); !strings.Contains(h, "REST route") {
		t.Fatalf("hover on //@rest = %q", h)
	}
	if h := HoverAt(src, Pos{Line: 1, Char: 2}); h != "" {
		t.Fatalf("hover off-annotation should be empty, got %q", h)
	}
}

func TestCompletions_PrimariesFirst(t *testing.T) {
	items := Completions()
	if len(items) != len(Catalog) {
		t.Fatalf("got %d items, want %d", len(items), len(Catalog))
	}
	// auth/use (modifiers) must come after every primary.
	seenModifier := false
	for _, it := range items {
		s := Catalog[it.Label]
		if s.Kind == Modifier {
			seenModifier = true
		} else if seenModifier {
			t.Fatalf("primary %q appeared after a modifier", it.Label)
		}
	}
}

func TestSemanticTokens_RestLine(t *testing.T) {
	// 0123456789...
	// "//@rest GET /users/:id"
	//    ^3 @ at 2, keyword "rest" at 3..7, GET at 8..11, path at 12..22
	src := "//@rest GET /users/:id\nfunc NewX() {}\n"
	toks := SemanticTokens(src)
	if len(toks) != 3 {
		t.Fatalf("got %d tokens, want 3: %+v", len(toks), toks)
	}
	// @rest : includes the '@', so starts at col 2, length 5 ("@rest").
	if k := toks[0]; k.Line != 0 || k.Char != 2 || k.Length != 5 || k.Type != TokKeyword {
		t.Errorf("keyword token = %+v, want {0 2 5 keyword}", k)
	}
	// GET -> constant/method
	if m := toks[1]; m.Char != 8 || m.Length != 3 || m.Type != TokMethod {
		t.Errorf("method token = %+v, want char 8 len 3 method", m)
	}
	// /users/:id -> string
	if p := toks[2]; p.Char != 12 || p.Length != 10 || p.Type != TokString {
		t.Errorf("path token = %+v, want char 12 len 10 string", p)
	}
	// GET carries the per-verb modifier so each method can color distinctly.
	if m := toks[1]; m.Modifiers != ModGet {
		t.Errorf("method modifier = %d, want ModGet (%d)", m.Modifiers, ModGet)
	}
}

func TestSemanticTokens_MethodModifierPerVerb(t *testing.T) {
	cases := map[string]int{
		"//@rest GET /x\nfunc N(){}\n":     ModGet,
		"//@rest post /x\nfunc N(){}\n":    ModPost, // any case
		"//@rest DELETE /x\nfunc N(){}\n":  ModDelete,
		"//@rest OPTIONS /x\nfunc N(){}\n": ModOptions,
	}
	for src, want := range cases {
		toks := SemanticTokens(src)
		if len(toks) < 2 || toks[1].Type != TokMethod {
			t.Fatalf("%q: missing method token: %+v", src, toks)
		}
		if toks[1].Modifiers != want {
			t.Errorf("%q: modifier = %d, want %d", src, toks[1].Modifiers, want)
		}
	}
	// Auth verb (Required) uses the method TYPE but no HTTP modifier.
	toks := SemanticTokens("//@rest GET /x\n//@auth Required\nfunc N(){}\n")
	for _, tk := range toks {
		if tk.Type == TokMethod && tk.Line == 1 && tk.Modifiers != 0 {
			t.Errorf("auth verb should have no method modifier, got %d", tk.Modifiers)
		}
	}
}

func TestSemanticTokens_SpacedFormColumns(t *testing.T) {
	// gofmt'd: "// @query" — '@' at col 3, keyword at 4..9.
	src := "// @query\nfunc NewX() {}\n"
	toks := SemanticTokens(src)
	if len(toks) != 1 {
		t.Fatalf("got %d tokens, want 1: %+v", len(toks), toks)
	}
	if k := toks[0]; k.Char != 3 || k.Length != 6 || k.Type != TokKeyword { // "@query"
		t.Fatalf("spaced keyword token = %+v, want {char 3 len 6 keyword}", k)
	}
}

func TestSemanticTokens_QualifiedSplitsAtDot(t *testing.T) {
	// A custom //@pkg.Func must be split so no token spans the '.': @inertia
	// (keyword) + Page (function) + string args. Both //@ and gofmt'd "// @".
	for _, src := range []string{
		"//@inertia.Page \"GET\" \"/users\" \"Users/Index\"\nfunc NewU() {}\n",
		"// @inertia.Page \"GET\" \"/users\" \"Users/Index\"\nfunc NewU() {}\n",
	} {
		toks := SemanticTokens(src)
		if len(toks) < 2 {
			t.Fatalf("expected split keyword tokens, got %+v", toks)
		}
		kw, fn := toks[0], toks[1]
		if kw.Type != TokKeyword || fn.Type != TokFunction {
			t.Fatalf("want keyword then function, got types %d,%d", kw.Type, fn.Type)
		}
		// No token may cross the dot: kw must end before fn starts, with the
		// single '.' column between them.
		if kw.Char+kw.Length >= fn.Char {
			t.Fatalf("tokens span the dot: kw=%+v fn=%+v", kw, fn)
		}
		if fn.Length != 4 { // "Page"
			t.Fatalf("function token length = %d, want 4 (Page)", fn.Length)
		}
	}
}

func TestSemanticTokens_UseArgsUncolored(t *testing.T) {
	// //@use's expression is arbitrary Go — only the keyword is colored.
	src := "//@rest GET /x\n//@use ratelimit.PerUser(100, time.Minute)\nfunc NewX() {}\n"
	for _, tk := range SemanticTokens(src) {
		if tk.Line == 1 && tk.Type != TokKeyword {
			t.Fatalf("//@use arg should be uncolored, got token %+v", tk)
		}
	}
}

func findCode(ds []Diagnostic, code string) *Diagnostic {
	for i := range ds {
		if ds[i].Code == code {
			return &ds[i]
		}
	}
	return nil
}
