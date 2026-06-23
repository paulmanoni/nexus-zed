package nexustoml

import (
	"strings"
	"testing"
)

func codes(ds []Diagnostic) map[string]Diagnostic {
	m := map[string]Diagnostic{}
	for _, d := range ds {
		m[d.Code] = d
	}
	return m
}

func TestUnknownKeyWithSuggestion(t *testing.T) {
	src := "[runtime]\nintrospecton = true\n" // typo: introspecton
	d := codes(Diagnostics(src))
	got, ok := d["unknown-key"]
	if !ok {
		t.Fatalf("expected unknown-key diagnostic, got %v", d)
	}
	if !strings.Contains(got.Message, `"introspection"`) {
		t.Fatalf("expected did-you-mean introspection, got %q", got.Message)
	}
}

func TestMisplacedTopLevelRuntimeKey(t *testing.T) {
	src := "introspection = true\n[runtime]\n"
	d := codes(Diagnostics(src))
	if _, ok := d["misplaced-runtime-key"]; !ok {
		t.Fatalf("expected misplaced-runtime-key, got %v", d)
	}
}

func TestScopeEnumEnforced(t *testing.T) {
	src := "[runtime.server.listeners.admin]\nscope = \"administrator\"\n"
	d := codes(Diagnostics(src))
	if got, ok := d["bad-enum"]; !ok {
		t.Fatalf("expected bad-enum, got %v", d)
	} else if got.Severity != SevError {
		t.Fatalf("scope enum should be an error, got severity %d", got.Severity)
	}

	// A valid scope produces no diagnostic.
	if ds := Diagnostics("[runtime.server.listeners.admin]\nscope = \"admin\"\n"); len(ds) != 0 {
		t.Fatalf("valid scope should be clean, got %v", ds)
	}
}

func TestTypeMismatch(t *testing.T) {
	src := "[runtime]\nintrospection = \"yes\"\n" // bool expected
	d := codes(Diagnostics(src))
	if _, ok := d["type-mismatch"]; !ok {
		t.Fatalf("expected type-mismatch for bool key, got %v", d)
	}
}

func TestBadDurationAndCIDR(t *testing.T) {
	dur := codes(Diagnostics("[runtime.middleware.cors]\nmax_age = \"12hh\"\n"))
	if _, ok := dur["bad-duration"]; !ok {
		t.Fatalf("expected bad-duration, got %v", dur)
	}
	if ds := Diagnostics("[runtime.middleware.cors]\nmax_age = \"12h\"\n"); len(ds) != 0 {
		t.Fatalf("valid duration should be clean, got %v", ds)
	}
	cidr := codes(Diagnostics("[runtime]\nintrospection_networks = [\"10.0.0.0/8\", \"nope\"]\n"))
	if _, ok := cidr["bad-cidr"]; !ok {
		t.Fatalf("expected bad-cidr, got %v", cidr)
	}
}

func TestNegativeRateLimit(t *testing.T) {
	d := codes(Diagnostics("[runtime.middleware.ratelimit]\nrpm = -5\n"))
	if got, ok := d["bad-ratelimit"]; !ok || got.Severity != SevError {
		t.Fatalf("expected bad-ratelimit error, got %v", d)
	}
}

func TestOpenSectionsNotFlagged(t *testing.T) {
	// [env] and custom [extensions.*] accept arbitrary keys.
	src := "[env.client]\nid = \"web\"\n[extensions.mycustom]\nfoo = 1\n"
	if ds := Diagnostics(src); len(ds) != 0 {
		t.Fatalf("open sections should not be flagged, got %v", ds)
	}
}

func TestUnknownRuntimeSection(t *testing.T) {
	d := codes(Diagnostics("[runtime.dashbaord]\nenabled = true\n"))
	got, ok := d["unknown-section"]
	if !ok {
		t.Fatalf("expected unknown-section, got %v", d)
	}
	if !strings.Contains(got.Message, "dashboard") {
		t.Fatalf("expected did-you-mean dashboard, got %q", got.Message)
	}
}

func TestArbitraryTopLevelSectionAllowed(t *testing.T) {
	// nexus.Get can read arbitrary sections; don't flag them.
	if ds := Diagnostics("[mycompany.feature]\nflag = true\n"); len(ds) != 0 {
		t.Fatalf("arbitrary section should be clean, got %v", ds)
	}
}

func TestIntermediateParentTableAllowed(t *testing.T) {
	// [runtime.server.listeners] is a legal parent of the wildcard spec.
	d := codes(Diagnostics("[runtime.server.listeners]\n"))
	if _, ok := d["unknown-section"]; ok {
		t.Fatalf("intermediate parent table should not be flagged: %v", d)
	}
}

func TestKeyCompletionInSection(t *testing.T) {
	src := "[runtime.dashboard]\n\n"
	items := Completions(src, Pos{Line: 1, Char: 0})
	var hasEnabled, hasName bool
	for _, it := range items {
		if it.Kind != "key" {
			t.Fatalf("expected key completions, got kind %q", it.Kind)
		}
		switch it.Label {
		case "enabled":
			hasEnabled = true
		case "name":
			hasName = true
		}
	}
	if !hasEnabled || !hasName {
		t.Fatalf("expected enabled+name, got %+v", items)
	}
}

func TestKeyCompletionExcludesPresent(t *testing.T) {
	src := "[runtime.dashboard]\nenabled = true\n\n"
	for _, it := range Completions(src, Pos{Line: 2, Char: 0}) {
		if it.Label == "enabled" {
			t.Fatalf("present key should be excluded from completion")
		}
	}
}

func TestSectionHeaderCompletion(t *testing.T) {
	items := Completions("[", Pos{Line: 0, Char: 1})
	var found bool
	for _, it := range items {
		if it.Kind != "section" {
			t.Fatalf("expected section kind, got %q", it.Kind)
		}
		if it.Label == "runtime.dashboard" && it.InsertText == "runtime.dashboard]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected runtime.dashboard section completion, got %+v", items)
	}
}

func TestEnumValueCompletion(t *testing.T) {
	src := "[runtime.server.listeners.admin]\nscope = "
	items := Completions(src, Pos{Line: 1, Char: len("scope = ")})
	var vals []string
	for _, it := range items {
		if it.Kind != "value" {
			t.Fatalf("expected value kind, got %q", it.Kind)
		}
		vals = append(vals, it.InsertText)
	}
	if len(vals) != 3 || !contains(vals, `"admin"`) {
		t.Fatalf("expected scope enum values, got %v", vals)
	}
}

func TestHoverKeyAndSection(t *testing.T) {
	src := "[runtime]\nintrospection = true\n"
	if h := HoverAt(src, Pos{Line: 0, Char: 3}); !strings.Contains(h, "Runtime configuration") {
		t.Fatalf("section hover missing, got %q", h)
	}
	if h := HoverAt(src, Pos{Line: 1, Char: 4}); !strings.Contains(h, "dashboard") {
		t.Fatalf("key hover missing, got %q", h)
	}
}

func TestSymbolsOutline(t *testing.T) {
	src := "[runtime]\nintrospection = true\n[databases.main]\ndriver = \"postgres\"\n"
	syms := Symbols(src)
	if len(syms) != 2 || syms[0].Name != "[runtime]" || syms[1].Name != "[databases.main]" {
		t.Fatalf("unexpected outline: %+v", syms)
	}
}

func TestCommentAndInlineCommentTolerated(t *testing.T) {
	src := "# a comment\n[runtime]\nintrospection = true # turn it on\n"
	if ds := Diagnostics(src); len(ds) != 0 {
		t.Fatalf("comments should not break parsing, got %v", ds)
	}
}
