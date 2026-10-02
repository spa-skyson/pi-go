package palace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultAPIEmbedModel is the model name sent to an OpenAI-compatible
	// embeddings endpoint when none is configured. It matches the deployment
	// this backend exists for (#44): bge-m3 behind vLLM, TEI, or a gateway.
	// A wrong name surfaces as a request error, never as mixed vectors — one
	// endpoint serves one model.
	DefaultAPIEmbedModel = "bge-m3"

	// apiEmbedBatchSize is how many texts are sent per /embeddings call.
	// Deliberately far below ollamaEmbedBatchSize: an external gateway can sit
	// behind rate limits and payload caps the client cannot see, and a smaller
	// request keeps a rejection cheap. Large batches buy little over the wire.
	apiEmbedBatchSize = 64

	// apiEmbedTimeout bounds one /embeddings request. Generous enough for a
	// 64-text batch over a remote gateway; short enough that a dead endpoint
	// does not stall a mining run for minutes.
	apiEmbedTimeout = 30 * time.Second

	// maxAPIResponseBytes bounds one response body: 64 embeddings of up to
	// ~2048 dimensions at ~12 bytes each is well under 1MB; the cap only stops
	// a misbehaving endpoint from exhausting memory before the error path runs.
	maxAPIResponseBytes = 8 << 20
)

// apiEmbedder embeds text through an external OpenAI-compatible
// /v1/embeddings endpoint (#44): vLLM, TEI, LM Studio, or a corporate gateway
// in front of them.
//
// Unlike ollamaEmbedder there is no startup probe: OpenAI-compatible servers
// do not uniformly implement /models, so probing one would wrongly demote
// valid endpoints to the fallback chain. Misconfiguration surfaces at the
// first Embed call instead, and callers degrade the same way they do for
// ollama errors — search falls back to FTS5, mining aborts with the cause.
type apiEmbedder struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// NewAPIEmbedder targets an OpenAI-compatible embeddings endpoint. baseURL is
// required and is the root up to (not including) /embeddings — e.g.
// "http://llm.internal:8000/v1". An empty model uses DefaultAPIEmbedModel.
// An empty apiKey sends no Authorization header.
//
// Construction verifies only the URL: there is deliberately no network probe,
// see the type comment. The key is never logged.
func NewAPIEmbedder(baseURL, model, apiKey string) (Embedder, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("palace: api embedder: empty embeddings url")
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("palace: bad embeddings url %q: %w", baseURL, err)
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultAPIEmbedModel
	}
	return &apiEmbedder{
		baseURL: baseURL,
		model:   model,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: apiEmbedTimeout},
	}, nil
}

// embeddingsRequest is the OpenAI /v1/embeddings request body.
type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embeddingsResponse is the OpenAI /v1/embeddings response body. encoding/json
// decodes the vector's numbers as float64; conversion to the storage type
// float32 happens in embedBatch.
type embeddingsResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// Embed sends texts in batches and returns their vectors in order.
//
// A failed batch is returned as an error, never as nil vectors: a drawer
// stored without a vector is invisible to every later semantic search over it
// and the failure is silent at query time. The miner aborts on this, search
// falls back to FTS5.
func (a *apiEmbedder) Embed(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += apiEmbedBatchSize {
		end := min(start+apiEmbedBatchSize, len(texts))

		vecs, err := a.embedBatch(texts[start:end])
		if err != nil {
			// The endpoint URL is configuration, not a secret, and never
			// carries the key — so naming it in errors is safe.
			return nil, fmt.Errorf("palace: api embed (%s): %w", a.baseURL, err)
		}
		if got, want := len(vecs), end-start; got != want {
			return nil, fmt.Errorf("palace: api embed returned %d embeddings for %d inputs", got, want)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// embedBatch sends one request and returns the batch's vectors in input order.
func (a *apiEmbedder) embedBatch(batch []string) ([][]float32, error) {
	body, err := json.Marshal(embeddingsRequest{Model: a.model, Input: batch})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), apiEmbedTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post /embeddings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncateForError(data))
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Data) != len(batch) {
		return nil, fmt.Errorf("response carries %d embeddings for %d inputs", len(parsed.Data), len(batch))
	}

	// OpenAI guarantees data[i].index == i, but decode by index anyway so a
	// server that reorders still yields one vector per input, in order.
	out := make([][]float32, len(batch))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(out) {
			return nil, fmt.Errorf("response index %d out of range", item.Index)
		}
		if item.Embedding == nil {
			return nil, fmt.Errorf("response missing embedding for input %d", item.Index)
		}
		vec := make([]float32, len(item.Embedding))
		for i, f := range item.Embedding {
			vec[i] = float32(f)
		}
		out[item.Index] = vec
	}
	return out, nil
}

// truncateForError keeps the first bytes of a response body for an error
// message, dropping newlines so log lines stay single-line.
func truncateForError(b []byte) string {
	const max = 256
	s := string(b)
	if len(s) > max {
		s = s[:max]
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
}

// Close is a no-op: the endpoint owns the model's lifetime, not this process.
func (a *apiEmbedder) Close() {}
