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

func TestAPIEmbedder_ClientTimeoutIsBounded(t *testing.T) {
	ts := newAPITestServer(t, http.StatusOK, 4)
	e, err := NewAPIEmbedder(ts.URL, "test-model", "")
	if err != nil {
		t.Fatalf("NewAPIEmbedder: %v", err)
	}
	a := e.(*apiEmbedder)
	if a.client.Timeout != apiEmbedTimeout || apiEmbedTimeout > 30*time.Second {
		t.Errorf("client timeout = %v, want the bounded %v", a.client.Timeout, apiEmbedTimeout)
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

func TestEmbedderAvailability_APIBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIEmbedderURL = "http://llm.internal:8000/v1"
	if err := EmbedderAvailability(cfg); err != nil {
		t.Errorf("availability with a valid embeddings_url: %v", err)
	}

	// The ollama branch must not be consulted while api is selected.
	cfg.UseOllama = false
	if err := EmbedderAvailability(cfg); err != nil {
		t.Errorf("api selection should not require the local model: %v", err)
	}
}

func TestAPIEmbedder_ImplementsEmbedder(t *testing.T) {
	var _ Embedder = (*apiEmbedder)(nil)
}
