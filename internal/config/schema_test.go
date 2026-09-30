package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spa-skyson/pi-rate/internal/permission"
)

// loadSchema reads the repo-root JSON schema guarding config.json's shape.
func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "config.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return doc
}

// jsonNames lists a struct's serialized field names: exported fields with a
// json tag that is neither empty nor "-".
func jsonNames(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := 0; i < t.NumField(); i++ {
		tag, ok := t.Field(i).Tag.Lookup("json")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// nodeAt walks the schema document: entries name a key under "properties",
// while "items" and "additionalProperties" descend into those members and
// "anyOf" expects the next step to be its branch index.
func nodeAt(t *testing.T, doc map[string]any, path []string) map[string]any {
	t.Helper()
	node := doc
	for i := 0; i < len(path); i++ {
		step := path[i]
		var next any
		switch {
		case step == "items" || step == "additionalProperties":
			next = node[step]
		case step == "anyOf":
			if i+1 >= len(path) {
				t.Fatalf("anyOf in path %v must be followed by a branch index", path)
			}
			idx, err := strconv.Atoi(path[i+1])
			if err != nil {
				t.Fatalf("anyOf index %q is not a number", path[i+1])
			}
			i++
			branches, ok := node["anyOf"].([]any)
			if !ok || idx >= len(branches) {
				t.Fatalf("schema node at %v has no anyOf[%d]", path[:i], idx)
			}
			next = branches[idx]
		default:
			props, ok := node["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema node at %v has no properties object", path)
			}
			next = props[step]
		}
		m, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("schema node at %v is missing (step %q)", path, step)
		}
		node = m
	}
	return node
}

// assertPropsMatch pins a schema object's property set to a Go struct's json
// tags: no missing keys, no extras. extras names schema-only keys allowed at
// that level (currently only the top-level "$schema").
func assertPropsMatch(t *testing.T, label string, node map[string]any, typ reflect.Type, extras ...string) {
	t.Helper()
	props, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: schema section has no properties object", label)
	}
	goNames := map[string]bool{}
	for _, n := range jsonNames(typ) {
		goNames[n] = true
	}
	extra := map[string]bool{}
	for _, e := range extras {
		extra[e] = true
	}
	// Every Config field must be documented by the schema...
	for n := range goNames {
		if _, ok := props[n]; !ok {
			t.Errorf("%s: Config field %q missing from schema properties", label, n)
		}
	}
	// ...and the schema must document nothing the struct does not serialize.
	for n := range props {
		if goNames[n] || extra[n] {
			continue
		}
		t.Errorf("%s: schema property %q has no matching Config field (stale or misspelled)", label, n)
	}
}

func TestSchema_ValidJSONAndClosedTopLevel(t *testing.T) {
	doc := loadSchema(t)
	if doc["additionalProperties"] != false {
		t.Errorf("top-level additionalProperties = %v, want false (unknown keys rejected)", doc["additionalProperties"])
	}
	nodeAt(t, doc, []string{"roles"}) // missing section fatals inside
}

func TestSchema_MatchesConfigStruct(t *testing.T) {
	doc := loadSchema(t)

	// Top level: every Config json tag present, nothing stale. "$schema" is
	// the one schema-only key: editors read it, unmarshal skips it.
	assertPropsMatch(t, "Config", doc, reflect.TypeOf(Config{}), "$schema")

	// One level deep for the key sections.
	for _, s := range []struct {
		label string
		path  []string
		typ   reflect.Type
	}{
		{"roles.*", []string{"roles", "additionalProperties"}, reflect.TypeOf(RoleConfig{})},
		{"providers.*", []string{"providers", "additionalProperties"}, reflect.TypeOf(ProviderConfig{})},
		{"providers.*.models.*", []string{"providers", "additionalProperties", "models", "additionalProperties"}, reflect.TypeOf(ProviderModelConfig{})},
		{"rateLimits.*", []string{"rateLimits", "additionalProperties"}, reflect.TypeOf(RateLimitConfig{})},
		{"memory", []string{"memory"}, reflect.TypeOf(MemoryConfig{})},
		{"palace", []string{"palace"}, reflect.TypeOf(PalaceConfig{})},
		{"compactor", []string{"compactor"}, reflect.TypeOf(CompactorConfig{})},
		{"autoCompact", []string{"autoCompact"}, reflect.TypeOf(AutoCompactConfig{})},
		{"hooks[]", []string{"hooks", "items"}, reflect.TypeOf(HookConfig{})},
		{"mcp.servers[]", []string{"mcp", "servers", "items"}, reflect.TypeOf(MCPServer{})},
		{"a2a.agents[]", []string{"a2a", "agents", "items"}, reflect.TypeOf(A2AAgentConfig{})},
		{"llms.sources[]", []string{"llms", "sources", "items"}, reflect.TypeOf(LLMSSource{})},
	} {
		assertPropsMatch(t, s.label, nodeAt(t, doc, s.path), s.typ)
	}
}

func TestSchema_PermissionMatchesRules(t *testing.T) {
	doc := loadSchema(t)

	// permission is serialized through Rules.MarshalJSON's flat shape, not a
	// plain struct: tool globs → directive, plus a reserved "bash" entry.
	perm := nodeAt(t, doc, []string{"permission"})
	props, ok := perm["properties"].(map[string]any)
	if !ok {
		t.Fatal("permission section has no properties object")
	}
	if _, ok := props["bash"]; !ok {
		t.Error("permission schema is missing the reserved \"bash\" key")
	}

	// Every extra key is a tool glob carrying a directive — its schema must
	// accept exactly the directives the package defines.
	addl, ok := perm["additionalProperties"].(map[string]any)
	if !ok {
		t.Fatal("permission additionalProperties must be a directive schema (flat opencode shape)")
	}
	definitions, ok := doc["definitions"].(map[string]any)
	if !ok {
		t.Fatal("schema has no definitions object")
	}
	def, ok := definitions["directive"].(map[string]any)
	if !ok {
		t.Fatal("definitions.directive is missing")
	}
	enumVals := map[string]bool{}
	for _, v := range def["enum"].([]any) {
		enumVals[v.(string)] = true
	}
	want := map[string]bool{
		string(permission.Allow): true,
		string(permission.Deny):  true,
		string(permission.Ask):   true,
	}
	if !reflect.DeepEqual(enumVals, want) {
		t.Errorf("definitions.directive enum = %v, want the permission package directives %v", enumVals, want)
	}
	if addl["$ref"] != "#/definitions/directive" {
		t.Errorf("permission additionalProperties = %v, want a $ref to #/definitions/directive", addl["$ref"])
	}

	// The bash array form: {pattern, directive} items.
	bashItems := nodeAt(t, doc, []string{"permission", "bash", "anyOf", "0", "items"})
	assertPropsMatch(t, "permission.bash[]", bashItems, reflect.TypeOf(permission.BashRule{}))
}

func TestConfigExample_Parses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config.example.jsonc"))
	if err != nil {
		t.Fatalf("read config.example.jsonc: %v", err)
	}
	clean, err := stripJSONC(raw)
	if err != nil {
		t.Fatalf("stripJSONC: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(clean, &cfg); err != nil {
		t.Fatalf("config.example.jsonc does not parse into Config: %v", err)
	}
	// The example documents these sections; an empty one means the example
	// drifted out of sync with what it claims to show.
	if len(cfg.Roles) == 0 || len(cfg.Providers) == 0 || cfg.Permission == nil ||
		cfg.MCP == nil || cfg.Memory == nil || cfg.DefaultAgent == "" {
		t.Error("config.example.jsonc no longer exercises roles, providers, permission, mcp, memory or defaultAgent")
	}
}
