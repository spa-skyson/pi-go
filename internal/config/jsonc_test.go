package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "comment at end of line",
			in:   "{\"a\": 1} // trailing\n",
			want: "{\"a\": 1} " + strings.Repeat(" ", len("// trailing")) + "\n",
		},
		{
			name: "comment in the middle of a line",
			in:   "{\n  // header\n  \"a\": 1 // mid /* not block */\n}\n",
			want: "{\n  " + strings.Repeat(" ", len("// header")) +
				"\n  \"a\": 1 " + strings.Repeat(" ", len("// mid /* not block */")) + "\n}\n",
		},
		{
			name: "comment-only file stays valid JSON",
			in:   "// just a comment\n{}",
			want: strings.Repeat(" ", len("// just a comment")) + "\n{}",
		},
		{
			name: "slashes inside a string value are kept",
			in:   "{\"cmd\": \"a // b\", \"n\": 2}",
			want: "{\"cmd\": \"a // b\", \"n\": 2}",
		},
		{
			name: "https URL in a string is kept",
			in:   "{\"url\": \"https://example.com/path\"}",
			want: "{\"url\": \"https://example.com/path\"}",
		},
		{
			name: "escaped quote does not close the string",
			in:   "{\"s\": \"quote \\\" then // still string\"}",
			want: "{\"s\": \"quote \\\" then // still string\"}",
		},
		{
			name: "escaped backslash before closing quote",
			in:   "{\"s\": \"ends with backslash\\\\\"} // real comment",
			want: "{\"s\": \"ends with backslash\\\\\"} " + strings.Repeat(" ", len("// real comment")),
		},
		{
			name: "comment on last line without newline",
			in:   "{\"a\": 1} // eof",
			want: "{\"a\": 1} " + strings.Repeat(" ", len("// eof")),
		},
		{
			name: "single slash is not a comment",
			in:   "{\"a\": 1/ 2 is invalid but untouched}",
			want: "{\"a\": 1/ 2 is invalid but untouched}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stripJSONC([]byte(tt.in))
			if err != nil {
				t.Fatalf("stripJSONC() error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("stripJSONC() =\n%q\nwant\n%q", got, tt.want)
			}
			if len(got) != len(tt.in) {
				t.Errorf("stripJSONC() changed length: %d -> %d (offsets must be preserved)", len(tt.in), len(got))
			}
		})
	}
}

func TestStripJSONC_PureJSONUnchanged(t *testing.T) {
	// Round-trip: valid JSON without comments must come back byte-identical.
	in := []byte(`{
  "roles": {"default": {"model": "gpt-4o"}},
  "theme": "dark",
  "baseURLs": {"ollama": "http://127.0.0.1:11434"},
  "url": "https://example.com"
}`)
	got, err := stripJSONC(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(in) {
		t.Errorf("plain JSON modified:\n%q\nwant\n%q", got, in)
	}
}

func TestStripJSONC_SyntaxErrorKeepsLineNumbers(t *testing.T) {
	// A syntax error three comment lines deep must still report the offset of
	// the original file: space replacement preserves byte offsets, so the
	// line computed from json.SyntaxError.Offset is the one the user wrote.
	src := "{\n" +
		"  // role for quick tasks\n" +
		"  // another note\n" +
		"  \"roles\": { \"default\": { \"model\": oops } },\n" +
		"  \"theme\": \"dark\"\n" +
		"}\n"
	clean, err := stripJSONC([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = json.Unmarshal(clean, &cfg)
	if err == nil {
		t.Fatal("expected a syntax error from the malformed config")
	}
	syn, ok := err.(*json.SyntaxError)
	if !ok {
		t.Fatalf("expected *json.SyntaxError, got %T: %v", err, err)
	}
	line := strings.Count(src[:syn.Offset], "\n") + 1
	if line != 4 {
		t.Errorf("syntax error reported offset %d = line %d, want line 4", syn.Offset, line)
	}
}

func TestLoadFile_WithComments(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	content := `{
	  // pi-go config — comments are allowed
	  "roles": {
	    "default": {"model": "gpt-4o"} // the main role
	  },
	  "theme": "dark",
	  "baseURLs": {
	    // LAN ollama; https in a value survives the comment stripper
	    "ollama": "https://ollama.internal:11434"
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := loadFile(cfgPath, &cfg); err != nil {
		t.Fatalf("loadFile with comments: %v", err)
	}
	if cfg.Roles["default"].Model != "gpt-4o" {
		t.Errorf("default role model = %q, want gpt-4o", cfg.Roles["default"].Model)
	}
	if cfg.Theme != "dark" {
		t.Errorf("theme = %q, want dark", cfg.Theme)
	}
	if cfg.BaseURLs["ollama"] != "https://ollama.internal:11434" {
		t.Errorf("baseURLs[ollama] = %q, URL inside a string was damaged", cfg.BaseURLs["ollama"])
	}
}

func TestLoadMCPServersFromFile_WithComments(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, "mcp.json")
	content := `{
	  "mcpServers": {
	    // docs server
	    "docs": {"url": "https://example.com/mcp"} // streamable HTTP
	  }
	}`
	if err := os.WriteFile(mcpPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	servers := loadMCPServersFromFile(mcpPath)
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(servers))
	}
	if servers[0].Name != "docs" || servers[0].URL != "https://example.com/mcp" {
		t.Errorf("unexpected server: %+v", servers[0])
	}
}
