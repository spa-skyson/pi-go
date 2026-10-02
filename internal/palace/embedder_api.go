package palace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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

	// apiEmbedMaxRetries is how many times a throttled or transiently failed
	// batch is retried — enough to ride out a burst of 429s behind a shared
	// gateway. Retry-After is deliberately not parsed: its granularity is
	// seconds, the pause already is one.
	apiEmbedMaxRetries = 2

	// apiProbeTimeout bounds the availability probe: one tiny request, so a
	// dead endpoint costs the miner seconds at startup, not the full request
	// budget.
	apiProbeTimeout = 5 * time.Second

	// maxAPIResponseBytes bounds one response body: a full batch of 64
	// embeddings at ~2048 dimensions and ~12 bytes per value is ~1.5MB; the
	// cap only stops a misbehaving endpoint from exhausting memory before the
	// error path runs.
	maxAPIResponseBytes = 8 << 20
)

// apiEmbedTimeout bounds one /embeddings request. Generous enough for a
// 64-text batch over a remote gateway; short enough that a dead endpoint
// does not stall a mining run for minutes. A var rather than a const so the
// timeout test can shrink it; nothing in production writes it.
var apiEmbedTimeout = 30 * time.Second

// apiEmbedRetryDelay is the pause between batch retries. A var rather than a
// const so tests can shrink it, mirroring ollamaEmbedRetryDelay; nothing in
// production writes it.
var apiEmbedRetryDelay = time.Second

// apiStatusError is a non-200 response from the endpoint. It carries the code
// so the retry loop and the availability probe can react without re-parsing
// the message.
type apiStatusError struct {
	code int
	body string
}

func (e *apiStatusError) Error() string { return fmt.Sprintf("status %d: %s", e.code, e.body) }

// retryableAPIStatus reports whether a response code is worth retrying: 429
// (throttled) and 5xx (a gateway or model server hiccup) are transient.
// Everything else — 401, 404, 400 — is configuration and fails identically
// until someone fixes it.
func retryableAPIStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// apiEmbedder embeds text through an external OpenAI-compatible
// /v1/embeddings endpoint (#44): vLLM, TEI, LM Studio, or a corporate gateway
// in front of them.
//
// Unlike ollamaEmbedder there is no startup probe inside openEmbedder:
// OpenAI-compatible servers do not uniformly implement /models, so probing
// one would wrongly demote valid endpoints to the fallback chain. The probe
// lives in ProbeAPIEmbedder for callers that cannot degrade — mining — and
// misconfiguration otherwise surfaces at the first Embed call, with callers
// degrading the same way they do for ollama errors — search falls back to
// FTS5, mining aborts with the cause.
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
// see the type comment. Every request is bounded by the context deadline
// embedBatch applies — the client carries no Timeout of its own. The key is
// never logged.
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
		client:  &http.Client{},
	}, nil
}

// ProbeAPIEmbedder reports whether an OpenAI-compatible endpoint answers a
// minimal embeddings request, with an actionable error when it does not.
// Mining gates on it before spending minutes on a run whose every batch would
// fail; openEmbedder deliberately does not — see the type comment.
func ProbeAPIEmbedder(baseURL, model, apiKey string) error {
	e, err := NewAPIEmbedder(baseURL, model, apiKey)
	if err != nil {
		return err
	}
	a := e.(*apiEmbedder)

	ctx, cancel := context.WithTimeout(context.Background(), apiProbeTimeout)
	defer cancel()
	if _, err := a.embedBatch(ctx, []string{"ping"}); err != nil {
		return probeError(a.baseURL, err)
	}
	return nil
}

// probeError turns a probe failure into the fix. Three causes dominate in
// practice — endpoint down, key rejected, wrong URL shape or model name — and
// each gets its own instruction. The key itself is never named.
func probeError(baseURL string, err error) error {
	var statusErr *apiStatusError
	switch {
	case errors.As(err, &statusErr) && (statusErr.code == http.StatusUnauthorized || statusErr.code == http.StatusForbidden):
		return fmt.Errorf("embeddings endpoint %s rejected the API key (status %d) — check palace.embeddings_api_key", baseURL, statusErr.code)
	case errors.As(err, &statusErr) && statusErr.code == http.StatusNotFound:
		return fmt.Errorf("embeddings endpoint %s returned 404 — check embeddings_url (root up to /embeddings) and the model name", baseURL)
	case errors.As(err, &statusErr):
		return fmt.Errorf("embeddings endpoint %s rejected the request: %w", baseURL, err)
	default:
		return fmt.Errorf("embeddings endpoint %s unreachable — is it up? (%w)", baseURL, err)
	}
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
// and the failure is silent at query time. The miner does not abort on this —
// it warns and leaves the affected chunks unindexed, so a re-run of
// `pi memory mine` fills the gaps; search degrades to FTS5.
func (a *apiEmbedder) Embed(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += apiEmbedBatchSize {
		end := min(start+apiEmbedBatchSize, len(texts))

		vecs, err := a.embedBatch(context.Background(), texts[start:end])
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
// A throttled or transiently failed batch is retried up to apiEmbedMaxRetries
// times with apiEmbedRetryDelay between attempts.
func (a *apiEmbedder) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	body, err := json.Marshal(embeddingsRequest{Model: a.model, Input: batch})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var data []byte
	for attempt := 0; ; attempt++ {
		data, err = a.post(ctx, body)
		if err == nil {
			break
		}
		var statusErr *apiStatusError
		if !errors.As(err, &statusErr) || !retryableAPIStatus(statusErr.code) || attempt >= apiEmbedMaxRetries {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("post /embeddings: %w", ctx.Err())
		case <-time.After(apiEmbedRetryDelay):
		}
	}

	var parsed embeddingsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Data) != len(batch) {
		return nil, fmt.Errorf("response carries %d embeddings for %d inputs", len(parsed.Data), len(batch))
	}

	// OpenAI guarantees data[i].index == i, but decode by index anyway so a
	// server that reorders still yields one vector per input, in order. Every
	// slot must be filled exactly once and every value finite: a nil or
	// zero-dim vector, a repeated index, or a float64 that overflows float32
	// (1e300 → +Inf → NaN in the cosine) would otherwise be stored and poison
	// search silently.
	out := make([][]float32, len(batch))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(out) {
			return nil, fmt.Errorf("response index %d out of range", item.Index)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("response missing embedding for input %d", item.Index)
		}
		if out[item.Index] != nil {
			return nil, fmt.Errorf("response repeats index %d", item.Index)
		}
		vec := make([]float32, len(item.Embedding))
		for i, f := range item.Embedding {
			v := float32(f)
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, fmt.Errorf("response embedding for input %d carries a non-finite value at position %d", item.Index, i)
			}
			vec[i] = v
		}
		out[item.Index] = vec
	}
	return out, nil
}

// post sends one request and returns the response body. A non-200 response is
// an *apiStatusError carrying the code and the truncated body.
func (a *apiEmbedder) post(ctx context.Context, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, apiEmbedTimeout)
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
		return nil, &apiStatusError{code: resp.StatusCode, body: truncateForError(data)}
	}
	return data, nil
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
