package palace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// apiTestServer is an OpenAI-compatible /v1/embeddings double. It records the
// requests it saw and replies with one embedding per input, at the given
// dimension, with the response entries shuffled so index-based decoding is the
// only thing that can restore input order.
type apiTestServer struct {
	*httptest.Server

	mu       sync.Mutex
	paths    []string
	auth     []string
	bodies   []embeddingsRequest
	status   int
	dim      int
	wantAuth string // empty = no Authorization header expected
}

func newAPITestServer(t *testing.T, status, dim int) *apiTestServer {
	t.Helper()
	ts := &apiTestServer{status: status, dim: dim}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embeddingsRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		ts.mu.Lock()
		ts.paths = append(ts.paths, r.URL.Path)
		ts.auth = append(ts.auth, r.Header.Get("Authorization"))
		ts.bodies = append(ts.bodies, body)
		ts.mu.Unlock()

		if ts.status != http.StatusOK {
			http.Error(w, `{"error":{"message":"model overloaded"}}`, ts.status)
			return
		}

		resp := embeddingsResponse{Data: make([]struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}, len(body.Input))}
		for i := range body.Input {
			vec := make([]float64, ts.dim)
			for j := range vec {
				vec[j] = float64(i+1) * 0.1 // input-tagged so order is observable
			}
			resp.Data[i].Index = i
			resp.Data[i].Embedding = vec
		}
		// Shuffle entries: data[i] carries index j. The embedder must decode
		// by index, not by position.
		if len(resp.Data) > 1 {
			resp.Data[0], resp.Data[len(resp.Data)-1] = resp.Data[len(resp.Data)-1], resp.Data[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestAPIEmbedder_Success(t *testing.T) {
	ts := newAPITestServer(t, http.StatusOK, 4)
	e, err := NewAPIEmbedder(ts.URL, "test-model", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}

	texts := []string{"alpha", "beta", "gamma"}
	vecs, err := e.Embed(texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors for %d inputs", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != 4 {
			t.Errorf("vector %d: dim = %d, want 4", i, len(v))
		}
		// The server tags every element with the input's position, so a
		// correct value proves input order survived the response shuffle.
		if v[0] != float32(i+1)*0.1 {
			t.Errorf("vector %d[0] = %v, want %v (input order broken)", i, v[0], float32(i+1)*0.1)
		}
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.bodies) != 1 || ts.bodies[0].Model != "test-model" {
		t.Fatalf("request body = %+v, want one request with model test-model", ts.bodies)
	}
	if got, want := strings.Join(ts.bodies[0].Input, ","), "alpha,beta,gamma"; got != want {
		t.Errorf("input = %q, want %q", got, want)
	}
	if got, want := ts.paths[0], "/embeddings"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestAPIEmbedder_BGE_M3_DimensionPassesThrough(t *testing.T) {
	// bge-m3 serves 1024-dim vectors where the local MiniLM serves 384; the
	// backend must carry whatever the endpoint returns, unsliced.
	ts := newAPITestServer(t, http.StatusOK, 1024)
	e, err := NewAPIEmbedder(ts.URL, "bge-m3", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	vecs, err := e.Embed([]string{"one"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 1024 {
		t.Fatalf("got %d vectors, first dim %d; want 1 vector of dim 1024", len(vecs), len(vecs[0]))
	}
}

func TestAPIEmbedder_BearerKey(t *testing.T) {
	const key = "sk-super-secret-do-not-log"
	ts := newAPITestServer(t, http.StatusOK, 4)
	ts.wantAuth = "Bearer " + key
	e, err := NewAPIEmbedder(ts.URL, "test-model", key)
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	if _, err := e.Embed([]string{"x"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.auth) != 1 || ts.auth[0] != ts.wantAuth {
		t.Fatalf("Authorization = %q, want %q", ts.auth, ts.wantAuth)
	}
}

func TestAPIEmbedder_NoKeySendsNoAuthHeader(t *testing.T) {
	ts := newAPITestServer(t, http.StatusOK, 4)
	e, err := NewAPIEmbedder(ts.URL, "test-model", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	if _, err := e.Embed([]string{"x"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.auth[0] != "" {
		t.Errorf("Authorization = %q, want empty", ts.auth[0])
	}
}

func TestAPIEmbedder_ServerErrorDegrades(t *testing.T) {
	// The 500 is retried before it surfaces; shrink the pause so -count=3
	// stays quick.
	old := apiEmbedRetryDelay
	apiEmbedRetryDelay = time.Millisecond
	t.Cleanup(func() { apiEmbedRetryDelay = old })

	ts := newAPITestServer(t, http.StatusInternalServerError, 4)
	e, err := NewAPIEmbedder(ts.URL, "test-model", "sk-secret")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	_, err = e.Embed([]string{"x"})
	if err == nil {
		t.Fatal("Embed succeeded against a 500 endpoint, want error")
	}
	// Never leak the key through the error path.
	if strings.Contains(err.Error(), "sk-secret") {
		t.Errorf("error message carries the API key: %v", err)
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error does not name the status: %v", err)
	}
}

func TestAPIEmbedder_ErrorBodyRedactsBearerCredentials(t *testing.T) {
	// A hostile or compromised gateway can echo the client's Authorization
	// header back in an error body (#44). The body reaches logs and the TUI
	// through apiStatusError, so both the exact key and the Bearer pattern
	// must be scrubbed before the body becomes an error message.
	const key = "sk-emb_k3y-9f2XqL7w"
	echoBody := `{"error":"invalid token: Bearer ` + key + `"}`

	tests := []struct {
		name       string
		body       string
		wantGone   string // must not appear in the error
		wantInMsg  string // must appear in the error
		notInMsg   string // must not appear, even redacted
		wantRedact bool   // the body carries a credential → [REDACTED] expected
	}{
		{
			name:       "echoed key is redacted",
			body:       echoBody,
			wantGone:   key,
			wantInMsg:  "status 400",
			notInMsg:   "sk-emb_",
			wantRedact: true,
		},
		{
			name:      "body without credentials is not distorted",
			body:      `{"error":{"message":"model overloaded"}}`,
			wantInMsg: `{"error":{"message":"model overloaded"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, tt.body, http.StatusBadRequest)
			}))
			t.Cleanup(ts.Close)

			e, err := NewAPIEmbedder(ts.URL, "test-model", key)
			if err != nil {
				t.Fatalf("NewAPIEmbedder: %v", err)
			}
			// A 400 is not retryable, so Embed fails on the first batch.
			_, err = e.Embed([]string{"x"})
			if err == nil {
				t.Fatal("Embed succeeded against a 400 endpoint, want error")
			}
			msg := err.Error()
			if tt.wantGone != "" && strings.Contains(msg, tt.wantGone) {
				t.Errorf("error message leaks the credential: %v", err)
			}
			if !strings.Contains(msg, tt.wantInMsg) {
				t.Errorf("error %q does not contain %q", msg, tt.wantInMsg)
			}
			if tt.notInMsg != "" && strings.Contains(msg, tt.notInMsg) {
				t.Errorf("error %q leaks a key prefix %q", msg, tt.notInMsg)
			}
			if tt.wantRedact && !strings.Contains(msg, "[REDACTED]") {
				t.Errorf("error %q does not mark the redaction", msg)
			}
			if !tt.wantRedact && strings.Contains(msg, "[REDACTED]") {
				t.Errorf("clean error body %q was needlessly redacted", msg)
			}
		})
	}
}

func TestAPIEmbedder_BatchesLargeInputs(t *testing.T) {
	ts := newAPITestServer(t, http.StatusOK, 4)
	e, err := NewAPIEmbedder(ts.URL, "test-model", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	texts := make([]string, apiEmbedBatchSize+2) // one full batch plus a remainder
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	vecs, err := e.Embed(texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(texts))
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.bodies) != 2 {
		t.Fatalf("%d requests, want 2 (batch size %d)", len(ts.bodies), apiEmbedBatchSize)
	}
	if len(ts.bodies[1].Input) != 2 {
		t.Errorf("remainder batch carries %d inputs, want 2", len(ts.bodies[1].Input))
	}
}

func TestAPIEmbedder_EmptyInput(t *testing.T) {
	ts := newAPITestServer(t, http.StatusOK, 4)
	e, err := NewAPIEmbedder(ts.URL, "test-model", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	vecs, err := e.Embed(nil)
	if vecs != nil || err != nil {
		t.Fatalf("Embed(nil) = %v, %v; want nil, nil", vecs, err)
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.bodies) != 0 {
		t.Errorf("empty input produced %d requests", len(ts.bodies))
	}
}

// TestAPIEmbedder_RequestTimeoutIsEnforced replaces the old tautology that
// only re-read the client field: here the endpoint genuinely never answers and
// the request must fail on the context deadline within the configured budget.
func TestAPIEmbedder_RequestTimeoutIsEnforced(t *testing.T) {
	// The handler blocks until the test releases it, so the only way Embed can
	// return is the context deadline. (Blocking on r.Context() would deadlock
	// the cleanup: httptest.Server.Close waits for this handler to return, and
	// the server does not always notice the client-side cancellation.)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); slow.Close() })

	old := apiEmbedTimeout
	apiEmbedTimeout = 150 * time.Millisecond
	t.Cleanup(func() { apiEmbedTimeout = old })

	e, err := NewAPIEmbedder(slow.URL, "test-model", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}

	start := time.Now()
	_, err = e.Embed([]string{"x"})
	if err == nil {
		t.Fatal("Embed returned against a server that never answers, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Embed hung %v against a dead endpoint — the deadline is not enforced", elapsed)
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error = %v, want the context deadline", err)
	}
}

func TestNewAPIEmbedder_ArgumentDefaults(t *testing.T) {
	if _, err := NewAPIEmbedder("", "m", ""); err == nil {
		t.Error("empty URL accepted, want error")
	}
	ts := newAPITestServer(t, http.StatusOK, 4)
	e, err := NewAPIEmbedder(ts.URL+"/", "", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	a := e.(*apiEmbedder)
	if a.model != DefaultAPIEmbedModel {
		t.Errorf("model = %q, want default %q", a.model, DefaultAPIEmbedModel)
	}
	if a.baseURL != ts.URL {
		t.Errorf("baseURL = %q, want trailing slash trimmed: %q", a.baseURL, ts.URL)
	}
}

// TestSearch_IgnoresVectorsFromThePreviousModel is the dim-mismatch guard:
// vectors stored by the old model (384-dim MiniLM) must not surface as
// zero-similarity semantic results once the palace runs a 1024-dim endpoint
// (bge-m3). Re-mining replaces them; until then they are invisible to
// semantic ranking.
func TestSearch_IgnoresVectorsFromThePreviousModel(t *testing.T) {
	ctx := context.Background()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewSQLitePalaceStore(db)

	// The pre-switch world: a drawer embedded by MiniLM at 384 dims.
	old := &Drawer{
		ID:        "old-mini-384",
		Wing:      "w",
		Room:      "r",
		Content:   "totally unrelated prose about database migrations",
		CreatedAt: time.Now().UTC(),
		Embedding: make([]float32, 384), // dim 384, the old model's space
	}
	for i := range old.Embedding {
		old.Embedding[i] = 0.5
	}
	if err := store.InsertDrawer(ctx, old); err != nil {
		t.Fatalf("insert old drawer: %v", err)
	}

	// The post-switch embedder: an API endpoint serving 1024 dims.
	ts := newAPITestServer(t, http.StatusOK, 1024)
	e, err := NewAPIEmbedder(ts.URL, "bge-m3", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}

	ds := NewDrawerService(store, e, DefaultConfig())
	results, err := ds.Search(ctx, SearchQuery{Query: "query that matches nothing", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Drawer.ID == old.ID {
			t.Errorf("old-model drawer %q surfaced after the model switch (similarity %.3f) — stale vectors are being used", old.ID, r.Similarity)
		}
		if len(r.Drawer.Embedding) == 384 {
			t.Errorf("result %q carries a 384-dim vector", r.Drawer.ID)
		}
	}
}

func TestRankBySimilarity_DropsForeignDimension(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []EmbeddingRow{
		{DrawerID: "same-dim", Embedding: []float32{1, 0, 0}},
		{DrawerID: "old-model", Embedding: make([]float32, 384)},
		{DrawerID: "nil-vec"},
	}
	scored := RankBySimilarity(query, candidates, 10)
	if len(scored) != 1 || scored[0].DrawerID != "same-dim" {
		t.Errorf("got %+v, want only same-dim", scored)
	}
}

// --- backend selection ---

func TestBatchSizeAndNameForAPIEmbedder(t *testing.T) {
	e := &apiEmbedder{model: "bge-m3"}
	if got := batchSizeFor(e); got != apiEmbedBatchSize {
		t.Errorf("batchSizeFor(api) = %d, want %d", got, apiEmbedBatchSize)
	}
	if got, want := embedderName(e), "api/bge-m3"; got != want {
		t.Errorf("embedderName(api) = %q, want %q", got, want)
	}
}

// TestEmbedderPool_APIEmbedderIsSharedNotCloned mirrors the ollama guard: the
// fallback would build local embedders alongside the API one and mix vector
// dimensions inside one wing.
func TestEmbedderPool_APIEmbedderIsSharedNotCloned(t *testing.T) {
	shared := &apiEmbedder{model: "test"}
	p := &Palace{embedder: shared}

	embs, cleanup := embedderPool(p, 8)
	defer cleanup()

	if len(embs) != 1 || embs[0] != Embedder(shared) {
		t.Fatalf("embedderPool returned %d embedders, want the single shared api instance", len(embs))
	}
}

// TestOpenEmbedder_APIBackendWins pins the priority chain: an explicit
// embeddings_url selects the api backend ahead of Ollama, so removing the api
// branch from openEmbedder turns this test red.
func TestOpenEmbedder_APIBackendWins(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIEmbedderURL = "http://llm.internal:8000/v1"
	cfg.APIEmbedderModel = "bge-m3"
	cfg.APIEmbedderKey = "sk-not-logged"
	cfg.UseOllama = true // would be chosen without the api branch

	e := openEmbedder(cfg)
	if e == nil {
		t.Fatal("openEmbedder returned nil with embeddings_url set")
	}
	a, ok := e.(*apiEmbedder)
	if !ok {
		t.Fatalf("openEmbedder returned %T, want *apiEmbedder (api must outrank ollama)", e)
	}
	if a.model != "bge-m3" || a.apiKey != "sk-not-logged" {
		t.Errorf("api embedder carries model %q key %q", a.model, a.apiKey)
	}
}

// TestOpenEmbedder_DefaultsUnchanged pins requirement 5: without any new
// config key the behavior is exactly what it was — no api backend, ollama/
// local decision untouched.
func TestOpenEmbedder_DefaultsUnchanged(t *testing.T) {
	def := DefaultConfig()
	if def.APIEmbedderURL != "" || def.APIEmbedderModel != "" || def.APIEmbedderKey != "" {
		t.Fatalf("DefaultConfig grew api fields: %+v", def)
	}

	// No api URL, ollama off, no model file → nil, the pre-change outcome.
	cfg := DefaultConfig()
	cfg.UseOllama = false
	if e := openEmbedder(cfg); e != nil {
		t.Fatalf("openEmbedder(defaults, ollama off) = %T, want nil (FTS5 degradation)", e)
	}
}

// TestAPIEmbedder_RejectsDegenerateResponses pins the response validation:
// every malformed payload below must come back as an error, never as a stored
// nil/zero-dim/non-finite vector — each of those poisons search silently (a
// nil vector passes the dim filter nowhere, a 1e300 value overflows float32 to
// +Inf and NaNs the cosine).
func TestAPIEmbedder_RejectsDegenerateResponses(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		inputs int
		want   string
	}{
		{
			name:   "duplicate index",
			body:   `{"data":[{"index":0,"embedding":[0.1,0.1]},{"index":0,"embedding":[0.2,0.2]}]}`,
			inputs: 2,
			want:   "repeats index 0",
		},
		{
			name:   "empty embedding",
			body:   `{"data":[{"index":0,"embedding":[]}]}`,
			inputs: 1,
			want:   "missing embedding",
		},
		{
			name:   "null embedding",
			body:   `{"data":[{"index":0,"embedding":null}]}`,
			inputs: 1,
			want:   "missing embedding",
		},
		{
			name:   "1e300 overflows float32 to +Inf",
			body:   `{"data":[{"index":0,"embedding":[1e300,0.5,0.5,0.5]}]}`,
			inputs: 1,
			want:   "non-finite",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(ts.Close)

			e, err := NewAPIEmbedder(ts.URL, "test-model", "")
			if err != nil {
				t.Fatalf("NewAPIEmbedder: %v", err)
			}
			inputs := make([]string, tt.inputs)
			vecs, err := e.Embed(inputs)
			if err == nil {
				t.Fatalf("Embed accepted %s, want error", tt.name)
			}
			if vecs != nil {
				t.Errorf("Embed returned vectors %v alongside the error, want nil", vecs)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// TestAPIEmbedder_RetriesThrottledBatch pins the retry contract: one 429 is
// ridden out and the batch succeeds on the second attempt; a 401 is
// configuration and fails on the first attempt without retries.
func TestAPIEmbedder_RetriesThrottledBatch(t *testing.T) {
	old := apiEmbedRetryDelay
	apiEmbedRetryDelay = time.Millisecond
	t.Cleanup(func() { apiEmbedRetryDelay = old })

	newCountingServer := func(t *testing.T, failFirst int, code int) (*httptest.Server, func() int) {
		t.Helper()
		var mu sync.Mutex
		calls := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			calls++
			attempt := calls
			mu.Unlock()
			if attempt <= failFirst {
				http.Error(w, `{"error":{"message":"come back later"}}`, code)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.5,0.5,0.5,0.5]}]}`))
		}))
		t.Cleanup(ts.Close)
		return ts, func() int {
			mu.Lock()
			defer mu.Unlock()
			return calls
		}
	}

	t.Run("429 then success", func(t *testing.T) {
		ts, calls := newCountingServer(t, 1, http.StatusTooManyRequests)
		e, err := NewAPIEmbedder(ts.URL, "test-model", "")
		if err != nil {
			t.Fatalf("NewAPIEmbedder: %v", err)
		}
		vecs, err := e.Embed([]string{"x"})
		if err != nil {
			t.Fatalf("Embed after one 429: %v", err)
		}
		if len(vecs) != 1 || len(vecs[0]) != 4 {
			t.Errorf("vecs = %v, want one 4-dim vector", vecs)
		}
		if got := calls(); got != 2 {
			t.Errorf("endpoint saw %d requests, want 2 (one 429, one retry)", got)
		}
	})

	t.Run("401 is not retried", func(t *testing.T) {
		ts, calls := newCountingServer(t, 10, http.StatusUnauthorized)
		e, err := NewAPIEmbedder(ts.URL, "test-model", "sk-secret")
		if err != nil {
			t.Fatalf("NewAPIEmbedder: %v", err)
		}
		_, err = e.Embed([]string{"x"})
		if err == nil || !strings.Contains(err.Error(), "status 401") {
			t.Fatalf("err = %v, want status 401", err)
		}
		if got := calls(); got != 1 {
			t.Errorf("endpoint saw %d requests, want 1 — an auth failure is not transient", got)
		}
	})
}

// TestEmbedderAvailability_APIBackend exercises the mining gate end to end: a
// live endpoint passes, and each common failure names its fix — unreachable,
// rejected key, wrong URL shape or model. openEmbedder is deliberately not
// probed (its fallback semantics are construction-only); this path is where
// the failure must be actionable.
func TestEmbedderAvailability_APIBackend(t *testing.T) {
	live := newAPITestServer(t, http.StatusOK, 4)
	badKey := newAPITestServer(t, http.StatusUnauthorized, 4)
	missing := newAPITestServer(t, http.StatusNotFound, 4)

	tests := []struct {
		name string
		url  string
		want string // empty = must succeed
	}{
		{name: "live endpoint", url: live.URL},
		{name: "unreachable endpoint", url: "http://127.0.0.1:1/v1", want: "unreachable"},
		{name: "rejected key", url: badKey.URL, want: "rejected the API key"},
		{name: "wrong url shape or model", url: missing.URL, want: "404"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.APIEmbedderURL = tt.url
			cfg.APIEmbedderModel = "bge-m3"
			cfg.UseOllama = true // the api choice must preempt ollama entirely

			err := EmbedderAvailability(cfg)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("availability with a live endpoint: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestAPIEmbedder_ImplementsEmbedder(t *testing.T) {
	var _ Embedder = (*apiEmbedder)(nil)
}
