package palace

const (
	defaultDeduplicationThreshold = float32(0.9)
	defaultDBPath                 = "palace.db"
)

// PalaceConfig holds configuration for the Palace system.
type PalaceConfig struct {
	DBPath                 string
	ModelPath              string
	IdentityFile           string
	DeduplicationThreshold float32
	L1TopK                 int
	L1MaxChars             int
	L2MaxDrawers           int
	L2MaxCharsPerDrawer    int

	// UseOllama selects the Ollama daemon for embedding instead of the
	// in-process model. Default on: it is an order of magnitude faster and
	// retrieves better. See DefaultOllamaEmbedModel for the measurements.
	UseOllama bool
	// OllamaURL is the daemon address; empty means DefaultOllamaURL.
	OllamaURL string
	// OllamaModel is the embedding model; empty means DefaultOllamaEmbedModel.
	OllamaModel string

	// APIEmbedderURL points at an external OpenAI-compatible embeddings
	// endpoint (root up to /embeddings, e.g. "http://llm.internal:8000/v1").
	// Non-empty selects the api backend ahead of Ollama and the in-process
	// model — see openEmbedder. Changing it (or switching away) swaps vector
	// spaces, so stored vectors must be re-mined, exactly like OllamaModel.
	APIEmbedderURL string
	// APIEmbedderModel is the model name sent to the endpoint; empty means
	// DefaultAPIEmbedModel. Changing it invalidates every stored vector.
	APIEmbedderModel string
	// APIEmbedderKey is the Bearer token for the endpoint; empty sends no
	// Authorization header. It is a secret: never log it.
	APIEmbedderKey string
}

// DefaultConfig returns a PalaceConfig with sensible defaults.
func DefaultConfig() PalaceConfig {
	return PalaceConfig{
		DBPath:                 defaultDBPath,
		DeduplicationThreshold: defaultDeduplicationThreshold,
		L1TopK:                 15,
		L1MaxChars:             3200,
		L2MaxDrawers:           10,
		L2MaxCharsPerDrawer:    300,
		UseOllama:              true,
		OllamaURL:              DefaultOllamaURL,
		OllamaModel:            DefaultOllamaEmbedModel,
	}
}

// Option is a functional option for configuring a Palace.
type Option func(*PalaceConfig)

// WithDBPath sets the database file path.
func WithDBPath(path string) Option {
	return func(c *PalaceConfig) { c.DBPath = path }
}

// WithModelPath sets the path to the embedding model directory.
func WithModelPath(path string) Option {
	return func(c *PalaceConfig) { c.ModelPath = path }
}

// WithIdentityFile sets the path to the L0 identity file.
func WithIdentityFile(path string) Option {
	return func(c *PalaceConfig) { c.IdentityFile = path }
}

// WithDeduplicationThreshold sets the cosine similarity threshold for duplicate detection.
func WithDeduplicationThreshold(t float32) Option {
	return func(c *PalaceConfig) { c.DeduplicationThreshold = t }
}

// WithOllamaEmbedder points the palace at an Ollama daemon for embedding.
// Empty arguments fall back to DefaultOllamaURL and DefaultOllamaEmbedModel.
func WithOllamaEmbedder(baseURL, model string) Option {
	return func(c *PalaceConfig) {
		c.UseOllama = true
		if baseURL != "" {
			c.OllamaURL = baseURL
		}
		if model != "" {
			c.OllamaModel = model
		}
	}
}

// WithAPIEmbedder points the palace at an external OpenAI-compatible
// embeddings endpoint. An empty url selects nothing; empty model and key fall
// back to DefaultAPIEmbedModel and no Authorization header.
func WithAPIEmbedder(url, model, key string) Option {
	return func(c *PalaceConfig) {
		if url != "" {
			c.APIEmbedderURL = url
		}
		if model != "" {
			c.APIEmbedderModel = model
		}
		c.APIEmbedderKey = key
	}
}

// WithLocalEmbedder forces the in-process model, bypassing Ollama. Used when no
// daemon is available and the caller would rather be slow than fail.
func WithLocalEmbedder() Option {
	return func(c *PalaceConfig) { c.UseOllama = false }
}
