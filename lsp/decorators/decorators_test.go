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

func findCode(ds []Diagnostic, code string) *Diagnostic {
	for i := range ds {
		if ds[i].Code == code {
			return &ds[i]
		}
	}
	return nil
}
