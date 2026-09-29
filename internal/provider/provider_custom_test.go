package provider

import (
	"context"
	"strings"
	"testing"
)

// A user-declared provider (config.json "providers") carries its wire
// protocol on Info.Protocol; NewLLM must route it onto the matching built-in
// client instead of rejecting the unknown provider name. The constructors
// dial nothing, so an unroutable URL is fine — it is never contacted here.
const customTestBaseURL = "http://127.0.0.1:9/v1"

func TestNewLLMCustomProtocolOpenAI(t *testing.T) {
	info := Info{Provider: "corp-claude", Model: "claude-opus-5", Custom: true, Protocol: "openai"}
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
	info := Info{Provider: "corp-claude", Model: "totally-unknown-model", Custom: true, Protocol: "openai"}
	if err := ValidateModel(info); err != nil {
		t.Fatalf("ValidateModel: %v", err)
	}
}
