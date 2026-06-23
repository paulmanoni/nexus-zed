// Package nexustoml is the nexus.toml half of the nexus Zed extension: it turns
// a nexus.toml config file into editor-friendly facts — diagnostics for unknown
// or misplaced keys, hover docs, an outline of the file's tables, and
// context-aware completion of sections, keys, and enum values.
//
// The schema mirrors nexus's own loader (runtime_config_loader.go,
// database_toml.go, extension/config) so the keys, types, and the few enforced
// enums match what the framework actually decodes at boot. It is transport-
// neutral: positions are plain 0-based line/character pairs so the LSP layer can
// map them to protocol types, and the logic stays unit-testable with no LSP
// machinery.
//
// The parser is deliberately tolerant of in-progress / invalid TOML (it never
// calls a real TOML decoder): it works line-by-line so the editor gets results
// mid-edit. It understands table headers ([a.b.c]) and `key = value` lines —
// which is the whole of nexus.toml — and ignores anything more exotic rather
// than erroring.
package nexustoml

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"
)

// Severity mirrors the LSP DiagnosticSeverity scale.
const (
	SevError   = 1
	SevWarning = 2
	SevInfo    = 3
	SevHint    = 4
)

// Pos is a 0-based line/character position; Range is a half-open [Start,End) span.
type Pos struct{ Line, Char int }
type Range struct{ Start, End Pos }

// Diagnostic is one problem found in a document.
type Diagnostic struct {
	Range    Range
	Severity int
	Message  string
	Code     string
}

// Symbol is one outline entry (a table header).
type Symbol struct {
	Name           string
	Detail         string
	Range          Range
	SelectionRange Range
}

// CompletionItem is a single suggestion. Kind: "key", "section", or "value".
type CompletionItem struct {
	Label      string
	Detail     string
	Doc        string
	InsertText string
	Kind       string
}

// VType is the value type a key expects.
type VType int

const (
	VString VType = iota
	VBool
	VInt
	VArray
	VDuration // a quoted Go duration string, e.g. "12h"
)

func (t VType) String() string {
	switch t {
	case VBool:
		return "bool"
	case VInt:
		return "int"
	case VArray:
		return "array"
	case VDuration:
		return "duration string"
	default:
		return "string"
	}
}

// KeySpec describes one known key inside a section.
type KeySpec struct {
	Name string
	Type VType
	Doc  string
	// Enum lists suggested string values (offered in completion). When
	// EnumStrict is true a value outside the list is also flagged as a
	// diagnostic — used only where nexus itself rejects the value (scope).
	Enum       []string
	EnumStrict bool
}

// SectionSpec describes one [table] in nexus.toml. Path may contain "*" for a
// wildcard name segment ([databases.<name>], [runtime.server.listeners.<name>]).
type SectionSpec struct {
	Path []string
	Doc  string
	Keys []KeySpec
	// Open sections accept arbitrary keys (no unknown-key diagnostics) —
	// [extensions.<custom>] and [env.*], whose contents nexus doesn't constrain.
	Open bool
}

var (
	scopeEnum = []string{"public", "admin", "internal"}
	envEnum   = []string{"development", "staging", "production"}
	drvEnum   = []string{"postgres", "mysql", "sqlite", "sqlserver"}
)

// Catalog is the authoritative nexus.toml schema, mirroring nexus's loaders.
var Catalog = []SectionSpec{
	{Path: []string{"runtime"}, Doc: "Runtime configuration consumed by nexus.Run at boot. All runtime keys live under [runtime] (or a [runtime.<sub>] table) — a runtime key at the top level is silently ignored.", Keys: []KeySpec{
		{Name: "environment", Type: VString, Enum: envEnum, Doc: "Deployment environment name — `development`, `staging`, or `production`. Drives per-environment overrides; empty normalizes to `production`."},
		{Name: "version", Type: VString, Doc: "Version string stamped on /__nexus/config for peer-skew detection. Usually set via `-ldflags`, not here."},
		{Name: "introspection", Type: VBool, Doc: "Master gate for the /__nexus dashboard + JSON APIs. Default false (they 404). Set true in dev; in prod prefer `introspection_networks`."},
		{Name: "introspection_networks", Type: VArray, Doc: "CIDR allowlist that bypasses the introspection gate, e.g. [\"10.0.0.0/8\"]. The TCP peer IP is matched (unspoofable)."},
		{Name: "trace_capacity", Type: VInt, Doc: "Request-trace ring-buffer size. 0 disables the Traces tab."},
		{Name: "sdk", Type: VBool, Doc: "One switch to generate + serve the typed client SDK. Takes effect only under `nexus dev` or when introspection is true."},
	}},
	{Path: []string{"runtime", "server"}, Doc: "HTTP server binding knobs.", Keys: []KeySpec{
		{Name: "addr", Type: VString, Doc: "HTTP listen address in single-listener mode (default \":8080\"). Ignored when [runtime.server.listeners.*] are declared."},
		{Name: "route_prefix", Type: VString, Doc: "Path prefix prepended to every user route (REST/GraphQL/WS). Leading slash required; framework routes (/__nexus, /health) are not prefixed."},
	}},
	{Path: []string{"runtime", "server", "listeners", "*"}, Doc: "A named multi-scope listener. The name in the header (e.g. [runtime.server.listeners.admin]) is the listener key.", Keys: []KeySpec{
		{Name: "addr", Type: VString, Doc: "Listen address for this listener. Empty auto-fills from the base addr (admin = port+1000, internal = port+2000)."},
		{Name: "scope", Type: VString, Enum: scopeEnum, EnumStrict: true, Doc: "Route scope for this listener: `public`, `admin`, or `internal`. Out-of-scope routes 404 on the listener."},
	}},
	{Path: []string{"runtime", "dashboard"}, Doc: "The /__nexus dashboard surface.", Keys: []KeySpec{
		{Name: "enabled", Type: VBool, Doc: "Mount /__nexus/* (Architecture / Endpoints / Crons / Rate-limits / Traces tabs + JSON API)."},
		{Name: "name", Type: VString, Doc: "Brand shown in the dashboard header and browser tab title. Defaults to \"Nexus\"."},
	}},
	{Path: []string{"runtime", "devreload"}, Doc: "Dev-mode (NEXUS_DEV=1) live-reload watcher tuning.", Keys: []KeySpec{
		{Name: "exclude", Type: VArray, Doc: "Glob patterns whose matches never trigger a live reload, e.g. [\"uploads\", \"*.tmp\"]. Dev-only."},
	}},
	{Path: []string{"runtime", "graphql"}, Doc: "Environment-level GraphQL knobs (apply across all services' schemas).", Keys: []KeySpec{
		{Name: "path", Type: VString, Doc: "Mount path for auto-generated GraphQL services (default \"/graphql\")."},
		{Name: "disable_playground", Type: VBool, Doc: "Turn OFF the in-browser GraphQL IDE (Apollo Sandbox). Enabled by default."},
		{Name: "debug", Type: VBool, Doc: "Skip query validation + response sanitization. Dev-only."},
		{Name: "pretty", Type: VBool, Doc: "Pretty-print JSON responses. Ship off in prod."},
		{Name: "document_cache_size", Type: VInt, Doc: "LRU size for the parse+validate memo. 0 = default 1024; a negative value disables the cache."},
	}},
	{Path: []string{"runtime", "middleware", "cors"}, Doc: "Built-in CORS policy. Omit the table for no CORS handling.", Keys: []KeySpec{
		{Name: "allow_origins", Type: VArray, Doc: "Allowed Origin values, e.g. [\"https://app.example.com\"]. [\"*\"] = any. Default [\"*\"]."},
		{Name: "allow_methods", Type: VArray, Doc: "Allowed cross-origin HTTP methods. Empty defaults to GET, POST, PUT, PATCH, DELETE, OPTIONS."},
		{Name: "allow_headers", Type: VArray, Doc: "Allowed request headers. Empty defaults to Origin, Content-Type, Accept, Authorization, X-Requested-With."},
		{Name: "expose_headers", Type: VArray, Doc: "Response headers JS may read. Empty omits the header."},
		{Name: "allow_credentials", Type: VBool, Doc: "Send Access-Control-Allow-Credentials: true. Cannot combine with allow_origins=[\"*\"] (browsers reject it)."},
		{Name: "max_age", Type: VDuration, Doc: "Preflight cache duration as a Go duration string (\"12h\", \"300s\"). Zero defaults to 12h."},
	}},
	{Path: []string{"runtime", "middleware", "ratelimit"}, Doc: "Built-in app-wide rate limit. Zero disables.", Keys: []KeySpec{
		{Name: "rpm", Type: VInt, Doc: "Requests per minute for the global rate limit. 0 disables."},
		{Name: "burst", Type: VInt, Doc: "Token-bucket burst capacity. 0 disables."},
	}},
	{Path: []string{"databases", "*"}, Doc: "A database connection ([databases.<name>]). Each value resolves inline-first, then via the config server's key_prefix. Wired in code with db.BindFromConfig[T](\"name\").", Keys: []KeySpec{
		{Name: "driver", Type: VString, Enum: drvEnum, Doc: "Database driver, e.g. `postgres`, `mysql`, `sqlite`."},
		{Name: "key_prefix", Type: VString, Doc: "Config-server key prefix to read connection secrets from (e.g. \"db.main\"), keeping secrets out of nexus.toml."},
		{Name: "sslmode", Type: VString, Doc: "TLS mode passed to the driver (e.g. \"disable\", \"require\")."},
		{Name: "timezone", Type: VString, Doc: "Session timezone, e.g. \"Africa/Dar_es_Salaam\"."},
		{Name: "schema", Type: VString, Doc: "Default schema / search_path."},
		{Name: "default", Type: VBool, Doc: "Mark this the default database (resolved when no name is given)."},
		{Name: "host", Type: VString, Doc: "Inline host. ${ENV} placeholders are expanded at load."},
		{Name: "port", Type: VString, Doc: "Inline port (a string in TOML, e.g. \"5432\")."},
		{Name: "user", Type: VString, Doc: "Inline user. ${ENV} expanded at load."},
		{Name: "password", Type: VString, Doc: "Inline password — prefer ${DB_PASSWORD} so the secret stays in the environment."},
		{Name: "name", Type: VString, Doc: "Inline database name."},
	}},
	{Path: []string{"extensions", "config"}, Doc: "The config-server extension ([extensions.config]). Provides hot-reloadable / remote nexus.Get values. Needs a blank import of extension/config.", Keys: []KeySpec{
		{Name: "endpoint", Type: VString, Doc: "Config-server URL (required), e.g. \"http://localhost:8078\"."},
		{Name: "identity", Type: VString, Doc: "Config-server identity / app name. Optional."},
		{Name: "profile", Type: VString, Doc: "Config profile to load. Optional, default \"default\"."},
		{Name: "poll_interval", Type: VDuration, Doc: "Hot-reload poll interval as a Go duration (\"30s\"). Optional."},
	}},
	{Path: []string{"extensions", "*"}, Doc: "A custom extension's config block ([extensions.<name>]). Decoded by that extension; keys are extension-defined.", Open: true},
	{Path: []string{"env"}, Doc: "The [env] bridge: every key becomes a process env var AND is exposed to the frontend as import.meta.env. Only put client-public values here — never a real server secret.", Open: true},
}

// runtimeScalarKeys are [runtime] scalar keys a user often mistakenly writes at
// the top level, where nexus silently ignores them. We flag that specifically.
var runtimeScalarKeys = map[string]bool{
	"environment": true, "version": true, "introspection": true,
	"introspection_networks": true, "trace_capacity": true, "sdk": true,
}

// --- parsing ---

type tomlKey struct {
	Name      string
	Dotted    bool // name contains a '.' (advanced nested key — skip key checks)
	Line      int
	NameStart int
	NameEnd   int
	ValRaw    string // trimmed, comment-stripped value text ("" if none)
	ValStart  int
	ValEnd    int
}

type tomlTable struct {
	Path        []string
	HeaderLine  int
	HeaderStart int // col of the first path char (just past '[')
	HeaderEnd   int // col of the closing ']'
	Keys        []tomlKey
}

type doc struct {
	Root   []tomlKey
	Tables []tomlTable
}

func splitLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func parse(text string) doc {
	lines := splitLines(text)
	var d doc
	cur := -1 // index into d.Tables, -1 = root
	for ln, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			// blank or comment-only
		case strings.HasPrefix(trimmed, "["):
			if tbl, ok := parseHeader(trimmed, indent, ln); ok {
				d.Tables = append(d.Tables, tbl)
				cur = len(d.Tables) - 1
			}
		default:
			if k, ok := parseKey(trimmed, indent, ln); ok {
				if cur < 0 {
					d.Root = append(d.Root, k)
				} else {
					d.Tables[cur].Keys = append(d.Tables[cur].Keys, k)
				}
			}
		}
	}
	return d
}

func parseHeader(trimmed string, indent, ln int) (tomlTable, bool) {
	open := 1
	if strings.HasPrefix(trimmed, "[[") { // array-of-tables; nexus doesn't use these
		open = 2
	}
	close := strings.IndexByte(trimmed, ']')
	if close <= open {
		return tomlTable{}, false
	}
	inner := trimmed[open:close]
	path := splitDotted(inner)
	if len(path) == 0 {
		return tomlTable{}, false
	}
	return tomlTable{
		Path:        path,
		HeaderLine:  ln,
		HeaderStart: indent + open,
		HeaderEnd:   indent + close,
	}, true
}

func parseKey(trimmed string, indent, ln int) (tomlKey, bool) {
	eq := indexOutsideQuotes(trimmed, '=')
	if eq < 0 {
		return tomlKey{}, false
	}
	left := trimmed[:eq]
	name := strings.TrimSpace(left)
	if name == "" {
		return tomlKey{}, false
	}
	unquoted := strings.Trim(name, `"'`)
	rightFull := trimmed[eq+1:]
	val := stripComment(rightFull)
	valTrim := strings.TrimSpace(val)
	leadWS := len(rightFull) - len(strings.TrimLeft(rightFull, " \t"))
	valStart := indent + eq + 1 + leadWS
	return tomlKey{
		Name:      unquoted,
		Dotted:    strings.Contains(unquoted, "."),
		Line:      ln,
		NameStart: indent,
		NameEnd:   indent + len(strings.TrimRight(left, " \t")),
		ValRaw:    valTrim,
		ValStart:  valStart,
		ValEnd:    valStart + len(valTrim),
	}, true
}

// splitDotted splits a dotted table path, honoring quoted segments so
// [env."a.b"] yields ["env", "a.b"].
func splitDotted(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := byte(0)
	flush := func() {
		seg := strings.TrimSpace(cur.String())
		seg = strings.Trim(seg, `"'`)
		if seg != "" {
			out = append(out, seg)
		}
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQ != 0:
			if c == inQ {
				inQ = 0
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			inQ = c
			cur.WriteByte(c)
		case c == '.':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

func indexOutsideQuotes(s string, target byte) int {
	inQ := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQ != 0 {
			if c == inQ {
				inQ = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inQ = c
		case target:
			return i
		}
	}
	return -1
}

func stripComment(s string) string {
	inQ := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQ != 0 {
			if c == inQ {
				inQ = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inQ = c
		case '#':
			return s[:i]
		}
	}
	return s
}

// --- schema lookup ---

func matchSpec(path []string) (SectionSpec, bool) {
	for _, sec := range Catalog {
		if pathMatch(sec.Path, path) {
			return sec, true
		}
	}
	return SectionSpec{}, false
}

func pathMatch(spec, path []string) bool {
	if len(spec) != len(path) {
		return false
	}
	for i := range spec {
		if spec[i] != "*" && spec[i] != path[i] {
			return false
		}
	}
	return true
}

// isSpecPrefix reports whether path is a strict parent of some spec path (e.g.
// ["runtime","server","listeners"] is the parent of the listeners wildcard).
// Such intermediate tables are legal headers, so they shouldn't be flagged.
func isSpecPrefix(path []string) bool {
	for _, sec := range Catalog {
		if len(path) >= len(sec.Path) {
			continue
		}
		ok := true
		for i := range path {
			if sec.Path[i] != "*" && sec.Path[i] != path[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func keySpec(sec SectionSpec, name string) (KeySpec, bool) {
	for _, k := range sec.Keys {
		if k.Name == name {
			return k, true
		}
	}
	return KeySpec{}, false
}

// --- diagnostics ---

// Diagnostics validates a nexus.toml document. It is conservative: it only flags
// things nexus genuinely mishandles — unknown/misplaced keys in closed sections,
// wrong value types, the one enforced enum (listener scope), unparseable
// durations / CIDRs, and negative rate limits. Open namespaces ([env], custom
// [extensions.*]) and arbitrary top-level sections are left alone.
func Diagnostics(text string) []Diagnostic {
	d := parse(text)
	var out []Diagnostic

	// Top-level keys: flag a [runtime] scalar written at the document root,
	// where nexus silently ignores it — a common, costly mistake.
	for _, k := range d.Root {
		if runtimeScalarKeys[k.Name] {
			out = append(out, Diagnostic{
				Range:    Range{Pos{k.Line, k.NameStart}, Pos{k.Line, k.NameEnd}},
				Severity: SevWarning,
				Message:  fmt.Sprintf("%q at the top level is ignored by nexus — move it under [runtime]", k.Name),
				Code:     "misplaced-runtime-key",
			})
		}
	}

	for _, tbl := range d.Tables {
		sec, matched := matchSpec(tbl.Path)
		hdrRange := Range{Pos{tbl.HeaderLine, tbl.HeaderStart}, Pos{tbl.HeaderLine, tbl.HeaderEnd}}

		if !matched {
			// Unknown header. Only flag inside the closed [runtime] namespace
			// (and only when it isn't a legal intermediate parent table).
			if len(tbl.Path) > 0 && tbl.Path[0] == "runtime" && !isSpecPrefix(tbl.Path) {
				msg := fmt.Sprintf("unknown nexus section [%s]", strings.Join(tbl.Path, "."))
				if sug := suggestSection(tbl.Path); sug != "" {
					msg += fmt.Sprintf("; did you mean [%s]?", sug)
				}
				out = append(out, Diagnostic{hdrRange, SevWarning, msg, "unknown-section"})
			}
			// Outside runtime, or an intermediate parent: keys are validated
			// against an empty/parent schema below only if we have a spec, which
			// we don't here — so skip key checks entirely.
			continue
		}
		if sec.Open {
			continue // [env], custom [extensions.*]: anything goes
		}
		for _, k := range tbl.Keys {
			if k.Dotted {
				continue // advanced nested key — don't guess
			}
			ks, ok := keySpec(sec, k.Name)
			nameRange := Range{Pos{k.Line, k.NameStart}, Pos{k.Line, k.NameEnd}}
			if !ok {
				msg := fmt.Sprintf("unknown key %q in [%s]", k.Name, strings.Join(tbl.Path, "."))
				if sug := suggestKey(sec, k.Name); sug != "" {
					msg += fmt.Sprintf("; did you mean %q?", sug)
				}
				out = append(out, Diagnostic{nameRange, SevWarning, msg, "unknown-key"})
				continue
			}
			out = append(out, valueChecks(k, ks, tbl.Path)...)
		}
	}
	return out
}

func valueChecks(k tomlKey, ks KeySpec, path []string) []Diagnostic {
	if k.ValRaw == "" {
		return nil
	}
	var out []Diagnostic
	valRange := Range{Pos{k.Line, k.ValStart}, Pos{k.Line, k.ValEnd}}
	kind := valKind(k.ValRaw)

	typeMismatch := func() {
		out = append(out, Diagnostic{valRange, SevWarning,
			fmt.Sprintf("%q expects %s", k.Name, ks.Type.String()), "type-mismatch"})
	}
	switch ks.Type {
	case VBool:
		if kind != kBool {
			typeMismatch()
		}
	case VInt:
		if kind != kInt {
			typeMismatch()
			break
		}
		// Negative rate-limit values are operator error (nexus rejects them).
		if (k.Name == "rpm" || k.Name == "burst") && strings.HasPrefix(k.ValRaw, "-") {
			out = append(out, Diagnostic{valRange, SevError,
				fmt.Sprintf("%s must be >= 0", k.Name), "bad-ratelimit"})
		}
	case VArray:
		if kind != kArray {
			typeMismatch()
			break
		}
		if k.Name == "introspection_networks" {
			for _, cidr := range arrayStrings(k.ValRaw) {
				if _, _, err := net.ParseCIDR(cidr); err != nil {
					out = append(out, Diagnostic{valRange, SevError,
						fmt.Sprintf("invalid CIDR %q in introspection_networks", cidr), "bad-cidr"})
					break
				}
			}
		}
	case VString:
		if kind != kString {
			typeMismatch()
			break
		}
		if ks.EnumStrict {
			v := unquote(k.ValRaw)
			if !contains(ks.Enum, v) {
				out = append(out, Diagnostic{valRange, SevError,
					fmt.Sprintf("%s must be one of %s", k.Name, quoteList(ks.Enum)), "bad-enum"})
			}
		}
	case VDuration:
		if kind != kString {
			typeMismatch()
			break
		}
		if d := unquote(k.ValRaw); d != "" {
			if _, err := time.ParseDuration(d); err != nil {
				out = append(out, Diagnostic{valRange, SevWarning,
					fmt.Sprintf("%s %q is not a valid Go duration (e.g. \"12h\", \"300s\")", k.Name, d), "bad-duration"})
			}
		}
	}
	return out
}

type valueKind int

const (
	kString valueKind = iota
	kBool
	kInt
	kArray
	kOther
)

func valKind(raw string) valueKind {
	if raw == "" {
		return kOther
	}
	switch {
	case raw == "true" || raw == "false":
		return kBool
	case raw[0] == '"' || raw[0] == '\'':
		return kString
	case raw[0] == '[':
		return kArray
	case isIntLiteral(raw):
		return kInt
	default:
		return kOther
	}
}

func isIntLiteral(s string) bool {
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i = 1
	}
	if i >= len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if (s[i] < '0' || s[i] > '9') && s[i] != '_' {
			return false
		}
	}
	return true
}

// arrayStrings pulls the quoted string elements out of a single-line array.
func arrayStrings(raw string) []string {
	var out []string
	inQ := byte(0)
	var cur strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inQ != 0 {
			if c == inQ {
				out = append(out, cur.String())
				cur.Reset()
				inQ = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQ = c
		}
	}
	return out
}

func unquote(s string) string { return strings.Trim(s, `"'`) }

// --- symbols (outline) ---

// Symbols returns one outline entry per table header.
func Symbols(text string) []Symbol {
	d := parse(text)
	out := []Symbol{}
	for _, tbl := range d.Tables {
		name := "[" + strings.Join(tbl.Path, ".") + "]"
		detail := ""
		if sec, ok := matchSpec(tbl.Path); ok {
			detail = firstSentence(sec.Doc)
		}
		r := Range{Pos{tbl.HeaderLine, 0}, Pos{tbl.HeaderLine, tbl.HeaderEnd + 1}}
		out = append(out, Symbol{Name: name, Detail: detail, Range: r, SelectionRange: r})
	}
	return out
}

// --- hover ---

// HoverAt returns markdown docs when pos sits on a known section header or key.
func HoverAt(text string, pos Pos) string {
	d := parse(text)
	for _, tbl := range d.Tables {
		if tbl.HeaderLine == pos.Line && pos.Char >= tbl.HeaderStart-1 && pos.Char <= tbl.HeaderEnd {
			if sec, ok := matchSpec(tbl.Path); ok {
				return fmt.Sprintf("**`[%s]`**\n\n%s", strings.Join(tbl.Path, "."), sec.Doc)
			}
			return ""
		}
		sec, matched := matchSpec(tbl.Path)
		for _, k := range tbl.Keys {
			if k.Line == pos.Line && pos.Char >= k.NameStart && pos.Char <= k.NameEnd {
				if matched {
					if ks, ok := keySpec(sec, k.Name); ok {
						return keyHover(tbl.Path, ks)
					}
				}
				return ""
			}
		}
	}
	return ""
}

func keyHover(path []string, ks KeySpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**`%s`** · _%s_ — in `[%s]`\n\n%s", ks.Name, ks.Type.String(), strings.Join(path, "."), ks.Doc)
	if len(ks.Enum) > 0 {
		fmt.Fprintf(&b, "\n\nValues: %s", quoteList(ks.Enum))
	}
	return b.String()
}

// --- completion ---

// Completions returns context-aware suggestions for the cursor position:
//   - on a header line ("[…") → section paths
//   - after `=` on an enum key → the allowed values
//   - otherwise, inside a table → that section's not-yet-present keys
func Completions(text string, pos Pos) []CompletionItem {
	lines := splitLines(text)
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	line := lines[pos.Line]
	if pos.Char > len(line) {
		pos.Char = len(line)
	}
	prefix := line[:pos.Char]
	trimmed := strings.TrimLeft(prefix, " \t")

	// Section-header context.
	if strings.HasPrefix(trimmed, "[") {
		return sectionCompletions()
	}

	// Enum-value context: cursor is to the right of `=` on a known enum key.
	if eq := indexOutsideQuotes(prefix, '='); eq >= 0 {
		name := strings.Trim(strings.TrimSpace(prefix[:eq]), `"'`)
		if sec, ok := matchSpec(tableAt(text, pos.Line)); ok {
			if ks, ok := keySpec(sec, name); ok && len(ks.Enum) > 0 {
				return enumCompletions(ks.Enum)
			}
		}
		return nil
	}

	// Key context: offer the enclosing section's remaining keys.
	path := tableAt(text, pos.Line)
	sec, ok := matchSpec(path)
	if !ok || sec.Open {
		return nil
	}
	present := presentKeys(text, path)
	out := []CompletionItem{}
	for _, ks := range sec.Keys {
		if present[ks.Name] {
			continue
		}
		detail := ks.Type.String()
		if len(ks.Enum) > 0 {
			detail += " · " + strings.Join(ks.Enum, " | ")
		}
		out = append(out, CompletionItem{
			Label:      ks.Name,
			Detail:     detail,
			Doc:        ks.Doc,
			InsertText: ks.Name + " = ",
			Kind:       "key",
		})
	}
	return out
}

func sectionCompletions() []CompletionItem {
	out := []CompletionItem{}
	for _, sec := range Catalog {
		label := strings.Join(sec.Path, ".")
		// Render wildcard segments as a readable placeholder.
		insert := label
		if strings.Contains(label, "*") {
			placeholder := "name"
			label = strings.ReplaceAll(label, "*", "<"+placeholder+">")
			insert = strings.ReplaceAll(strings.Join(sec.Path, "."), "*", placeholder)
		}
		out = append(out, CompletionItem{
			Label:      label,
			Detail:     firstSentence(sec.Doc),
			Doc:        sec.Doc,
			InsertText: insert + "]",
			Kind:       "section",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func enumCompletions(values []string) []CompletionItem {
	out := make([]CompletionItem, 0, len(values))
	for _, v := range values {
		out = append(out, CompletionItem{Label: v, InsertText: `"` + v + `"`, Kind: "value"})
	}
	return out
}

// tableAt returns the path of the table enclosing the given line (the nearest
// header at or above it), or nil for the root table.
func tableAt(text string, line int) []string {
	d := parse(text)
	var path []string
	for _, tbl := range d.Tables {
		if tbl.HeaderLine <= line {
			path = tbl.Path
		} else {
			break
		}
	}
	return path
}

func presentKeys(text string, path []string) map[string]bool {
	d := parse(text)
	out := map[string]bool{}
	for _, tbl := range d.Tables {
		if pathMatch(tbl.Path, path) || pathEqual(tbl.Path, path) {
			for _, k := range tbl.Keys {
				out[k.Name] = true
			}
		}
	}
	return out
}

func pathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- small helpers ---

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func quoteList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = `"` + x + `"`
	}
	return strings.Join(q, ", ")
}

func firstSentence(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i+1]
	}
	return s
}

func suggestKey(sec SectionSpec, name string) string {
	best, bestD := "", 3
	for _, k := range sec.Keys {
		if d := levenshtein(name, k.Name); d < bestD {
			best, bestD = k.Name, d
		}
	}
	return best
}

func suggestSection(path []string) string {
	target := strings.Join(path, ".")
	best, bestD := "", 4
	for _, sec := range Catalog {
		if strings.Contains(strings.Join(sec.Path, "."), "*") {
			continue
		}
		cand := strings.Join(sec.Path, ".")
		if d := levenshtein(target, cand); d < bestD {
			best, bestD = cand, d
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
