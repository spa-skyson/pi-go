package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// A user-declared provider (config.json "providers") carries its wire
// protocol on Info.Protocol; NewLLM must route it onto the matching built-in
// client instead of rejecting the unknown provider name. The constructors
// dial nothing, so an unroutable URL is fine — it is never contacted here.
const customTestBaseURL = "http://127.0.0.1:9/v1"

func TestNewLLMCustomProtocolOpenAICompatible(t *testing.T) {
	info := Info{Provider: "corp-claude", Model: "claude-opus-5", Custom: true, Protocol: "openai-compatible"}
	llm, err := NewLLM(context.Background(), info, "sk-test", customTestBaseURL, "none", nil)
	if err != nil {
		t.Fatalf("NewLLM: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM returned a nil LLM")
	}
}

func TestNewLLMCustomProtocolAnthropic(t *testing.T) {
	info := Info{Provider: "corp-proxy", Model: "claude-opus-5", Custom: true, Protocol: "anthropic"}
	llm, err := NewLLM(context.Background(), info, "sk-test", customTestBaseURL, "none", nil)
	if err != nil {
		t.Fatalf("NewLLM: %v", err)
	}
	if llm == nil {
		t.Fatal("NewLLM returned a nil LLM")
	}
}

// Custom without a protocol is the --url path's shape only when Provider is a
// known name; an unknown name with no protocol is still unsupported, and that
// must stay an error rather than silently routing somewhere.
func TestNewLLMCustomWithoutProtocol(t *testing.T) {
	info := Info{Provider: "corp-claude", Model: "claude-opus-5", Custom: true}
	_, err := NewLLM(context.Background(), info, "sk-test", customTestBaseURL, "none", nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported provider: corp-claude") {
		t.Fatalf("err = %v, want unsupported provider: corp-claude", err)
	}
}

// A declared provider's models are absent from every catalog; validation must
// pass them through the Custom escape hatch rather than reject the name.
func TestValidateModelCustomProtocol(t *testing.T) {
	info := Info{Provider: "corp-claude", Model: "totally-unknown-model", Custom: true, Protocol: "openai-compatible"}
	if err := ValidateModel(info); err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
}

// A protocol name nothing routes (the pre-openai-compatible spelling included)
// must fail loudly: a silent NewOpenAI fallback would drop the raw-base flag
// and send the request to a /v1 the endpoint may not have.
func TestNewLLMCustomUnknownProtocol(t *testing.T) {
	info := Info{Provider: "corp-claude", Model: "claude-opus-5", Custom: true, Protocol: "openai"}
	_, err := NewLLM(context.Background(), info, "sk-test", customTestBaseURL, "none", nil)
	if err == nil || !strings.Contains(err.Error(), `unsupported protocol "openai"`) {
		t.Fatalf("err = %v, want unsupported protocol %q openai", err, "openai")
	}
}

// End to end through NewLLM: an openai-compatible declared provider must reach
// <base>/chat/completions on the declared base, with no /v1 segment inserted —
// this is the zai-coding-plan /paas/v4 404 this file exists to prevent.
func TestNewLLMOpenAICompatKeepsRawBase(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "glm-5",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"},
			},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer srv.Close()

	info := Info{Provider: "zai-coding-plan", Model: "glm-5", Custom: true, Protocol: "openai-compatible"}
	llm, err := NewLLM(context.Background(), info, "sk-test", srv.URL+"/paas/v4", "", nil)
	if err != nil {
		t.Fatalf("NewLLM: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hi"}}},
		},
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}
	if gotPath != "/paas/v4/chat/completions" {
		t.Errorf("request path = %q, want /paas/v4/chat/completions (no /v1 inserted)", gotPath)
	}
}
