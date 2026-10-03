// Package retry classifies provider failures as retryable or terminal and
// paces the re-attempts.
//
// It exists because "is this worth retrying?" has to be answered identically in
// two places that cannot import each other: internal/provider, which retries a
// single failed HTTP request, and internal/agent, which retries a whole run.
//
// The classification is deliberately message-based. Providers hand pi-go their
// failures as opaque strings — an SSE stream error is a JSON body, not a typed
// error — so pattern matching on the text is the only signal available at both
// layers.
package retry

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config controls how many times a failed attempt is repeated and how long the
// pauses between attempts grow.
type Config struct {
	// MaxRetries is the number of retries after the initial attempt.
	MaxRetries int

	// InitialDelay is the pause before the first retry.
	InitialDelay time.Duration

	// MaxDelay caps the exponential backoff, and also caps any delay the
	// server itself asked for.
	MaxDelay time.Duration

	// Delays, when set, is an explicit pause schedule that replaces the
	// exponential backoff: retry attempt n waits Delays[n], and attempts past
	// the end of the slice reuse its last entry. A server-supplied hint still
	// wins, and MaxDelay still caps the result.
	Delays []time.Duration
}

// DefaultConfig returns the shared defaults.
//
// MaxDelay is a minute because the limits that produce a retryable 429 are
// per-minute windows: a shorter cap would guarantee the retry lands inside the
// same exhausted window it is waiting out.
func DefaultConfig() Config {
	return Config{
		MaxRetries:   5,
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
	}
}

// terminalPatterns are failures no amount of waiting will clear: the request,
// the credentials, or the account is wrong.
//
// These are matched *before* transientPatterns, because the most common failure
// in pi-go's own session history is a 429 that is not a rate limit at all —
// "you have reached your weekly usage limit" and "You exceeded your current
// quota" both arrive as 429 Too Many Requests. Classifying those by status code
// alone spends the full retry budget sleeping before reporting a failure the
// user has to fix by upgrading a plan.
var terminalPatterns = []string{
	// Quota and plan exhaustion, mostly wearing a 429.
	"usage limit",
	"upgrade for higher limits",
	"insufficient_quota",
	"exceeded your current quota",
	"check your plan and billing",
	"quota exceeded",
	// OpenAI's prepaid-balance code (credit_balance_exhausted), which its
	// Responses streams also send as a terminal error event.
	"credit_balance_exhausted",

	// HTTP 402 Payment Required prose. The bare status code is matched
	// separately, as a token — see status402Re. Matched before the transient
	// list, and a message carrying a retry window still wins (see
	// IsTransient): nothing matching only these patterns can carry one, but
	// the guard costs nothing.
	"payment required",

	// Authentication and authorization.
	"unauthorized",
	"forbidden",
	"invalid_api_key",
	"invalid api key",
	"incorrect api key",
	"token_expired",
	"insufficient permissions",
	"autherror",

	// Malformed or unroutable requests.
	"bad request",
	"invalid_request_error",
	"not found",

	// The sandbox denying the dial. Retrying re-denies it.
	"operation not permitted",
}

// transientPatterns are failures that a later identical request may survive.
var transientPatterns = []string{
	// Rate limiting proper — reached only if no terminal pattern matched.
	"429",
	"rate limit",
	"rate_limit",
	"too many requests",

	// Upstream faults.
	"500",
	"502",
	"503",
	"504",
	"internal server error",
	"server_error",
	"bad gateway",
	"service unavailable",
	"gateway timeout",
	"overloaded",
	"temporary failure",

	// Connection-level faults.
	"connection reset",
	"connection refused",
	"timeout",
	"timed out",
	"deadline exceeded",

	// The local socket or route went away under an open connection. macOS
	// reports EADDRNOTAVAIL as "read tcp 10.5.50.62:58742->104.18.2.115:443:
	// read: can't assign requested address" when the interface a stream was
	// bound to drops (VPN reconnect, Wi-Fi roam, sleep); Linux spells it
	// "cannot assign requested address". A fresh dial picks a live address.
	"can't assign requested address",
	"cannot assign requested address",
	"network is unreachable",
	"no route to host",
	"no such host",
	"server misbehaving",
	"temporary failure in name resolution",
	"broken pipe",
	"connection aborted",

	// A stream that died mid-flight. An HTTP/2 reset arrives as
	// "stream error: stream ID 9; INTERNAL_ERROR; received from peer", and a
	// truncated SSE body as "unexpected EOF" or "unexpected end of JSON
	// input" — none of which match any of the patterns above.
	"stream error:",
	"internal_error",
	"unexpected eof",
	"unexpected end of json input",

	// A stream that went silent — the provider's own idle timeout aborted it
	// (internal/provider idleStreamModel). Retryable by construction: the
	// request was already accepted once, and the observed TTFT failures this
	// guards against clear on a re-send (issue #37).
	"llm stream idle",
}

// status402Re matches HTTP 402 as a standalone status token: the code is
// neither preceded nor followed by a character that would make it part of a
// longer identifier. A bare substring pattern matched arbitrary text — a
// request id "14025", a model revision "gpt-402" — and classified those
// failures terminal. Spellings this must keep matching: "402" alone, "HTTP
// 402", `": 402 {"` in a JSON body, "status=402", "402 Payment Required".
var status402Re = regexp.MustCompile(`(^|[^0-9A-Za-z_-])402($|[^0-9A-Za-z_-])`)

// exhaustedTransientMarker is the prefix WithRetryContext (internal/agent)
// wraps a transient failure in when the retry budget is spent. It lives here
// rather than in internal/agent because the consumers that must act on it —
// the orchestrator's fallback chain and the TUI's model switcher — import
// internal/retry already and cannot import internal/agent without a cycle.
const exhaustedTransientMarker = "transient error after "

// ExhaustedTransientError builds the error WithRetryContext yields when a
// transient failure survived its full retry budget. Producer and consumer
// share this symbol, so the marker text cannot drift between them.
func ExhaustedTransientError(retries int, cause error) error {
	return fmt.Errorf("%s%d retries: %w", exhaustedTransientMarker, retries, cause)
}

// exhaustedTransientPrefixRe pins the marker to a full exhausted-budget
// wrapper: the digits and the " retries: " tail are what distinguish it from
// the partial-response verdict ("transient error after partial response (not
// retrying): …"), which is not an exhausted budget and must not match.
var exhaustedTransientPrefixRe = regexp.MustCompile(
	regexp.QuoteMeta(exhaustedTransientMarker) + `[0-9]+ retries: `)

// IsExhaustedTransient reports whether err is an exhausted retry budget over a
// transient provider failure — WithRetryContext's "transient error after N
// retries: <cause>" wrapper, possibly buried in a spawner's exit-error text
// ("pi process failed: exit status 1: error: agent run: transient error after
// 5 retries: …"). The underlying cause decides: a windowed rate limit (the
// server named when its window reopens) is not reported, so a caller keeps
// waiting that out instead of declaring the model dead. The cause is the
// wrapper line's remainder only — text below the first newline is other
// content's, not the cause's, and is ignored.
func IsExhaustedTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	i := strings.Index(msg, exhaustedTransientMarker)
	if i < 0 {
		return false
	}
	// The digits must follow — that is what "after partial response" lacks.
	if !exhaustedTransientPrefixRe.MatchString(msg[i:]) {
		return false
	}
	// Only the marker's own line decides: the digits end at the first newline,
	// so a multi-line stderr carrying an unrelated transient token below the
	// wrapper line cannot classify through its second line (issue #39 review).
	rest := msg[i+len(exhaustedTransientMarker):]
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	cause := errors.New(rest)
	// A windowed failure clears on its own; everything IsTransient calls
	// transient was worth retrying, so a different model is worth trying.
	return IsTransient(cause) && !HasServerWindow(cause)
}

// HasServerWindow reports whether err names a provider retry window — the
// server said when its failure clears, so a caller can wait it out instead of
// switching models.
func HasServerWindow(err error) bool {
	_, ok := ServerDelay(err)
	return ok
}

// IsTerminal reports whether err describes a failure that will recur
// identically however long the caller waits.
func IsTerminal(err error) bool {
	if err == nil {
		return false
	}
	// A server-supplied retry window means the failure clears when the window
	// reopens, so it is not terminal even when the prose otherwise reads like
	// quota exhaustion (see IsTransient for why that happens).
	if _, ok := ServerDelay(err); ok {
		return false
	}
	msg := err.Error()
	// 402 is matched as a status token rather than a substring: it is money,
	// not load, so no wait clears it (a 402 that names a retry window is
	// caught by the ServerDelay check above).
	if status402Re.MatchString(msg) {
		return true
	}
	return containsAny(strings.ToLower(msg), terminalPatterns)
}

// IsTransient reports whether err is worth retrying.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// A server-supplied retry window wins over everything else. Providers only
	// name a window ("retry in 59s", "retryDelay:59s") for failures that clear
	// — per-minute token limits, throttling — never for the quota/plan
	// exhaustion that arrives with terminal prose. This beats the terminal
	// patterns because Gemini's per-minute token limit reuses that prose
	// verbatim ("You exceeded your current quota", "quota exceeded", "check
	// your plan and billing") while still telling us exactly when its window
	// reopens.
	if _, ok := ServerDelay(err); ok {
		return true
	}

	// Terminal wins on a tie: a quota 429 matches both lists.
	if containsAny(msg, terminalPatterns) || status402Re.MatchString(msg) {
		return false
	}
	if containsAny(msg, transientPatterns) {
		return true
	}

	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}

	var tempErr interface{ Temporary() bool }
	if errors.As(err, &tempErr) && tempErr.Temporary() {
		return true
	}

	return false
}

func containsAny(msg string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// hintPrefix matches the phrases providers use to introduce a retry window,
// together with the punctuation that can sit between the phrase and the figure.
//
// The trailing `"?\s*:?\s*"?\s*` is what lets the same pattern read prose and
// raw JSON. Gemini states the window twice — once as English ("Please retry in
// 10.419145242s.") and once as a machine-readable RetryInfo detail — and the
// detail reaches pi-go as JSON when a transport reads the response body
// directly: `"retryDelay": "11s"`. Without tolerating the quotes and the
// spacing around the colon, the JSON form silently fails to match and the
// caller falls back to a fixed backoff that lands inside the same exhausted
// window.
const hintPrefix = `(?i)(?:try again in|retry[- ]after|retry in|retrydelay)"?\s*:?\s*"?\s*`

// durationHintRe matches a Go-parseable duration after a retry hint, covering
// the "9.422s" and "6m0.339s" forms OpenAI uses and the "59.440629838s" form
// Gemini uses.
var durationHintRe = regexp.MustCompile(
	hintPrefix + `((?:[0-9]+h)?(?:[0-9]+m)?[0-9]+(?:\.[0-9]+)?(?:ms|s|m|h))`)

// numberHintRe matches a bare number followed by an optional spelled-out unit,
// covering "retry after 30 seconds" and a raw Retry-After value.
var numberHintRe = regexp.MustCompile(
	hintPrefix + `([0-9]+(?:\.[0-9]+)?)\s*(milliseconds?|ms|seconds?|secs?|minutes?|mins?)?`)

// ServerDelay extracts the wait the provider asked for, if it named one.
//
// A rate-limit body usually carries the exact figure ("Please try again in
// 9.422s" from OpenAI, "Please retry in 59.440629838s" or a
// "retryDelay:59s" detail from Gemini), and honoring it is the difference
// between one well-timed retry and a backoff schedule that keeps landing
// inside the same exhausted window.
func ServerDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	msg := err.Error()

	if m := durationHintRe.FindStringSubmatch(msg); m != nil {
		if d, parseErr := time.ParseDuration(m[1]); parseErr == nil && d > 0 {
			return d, true
		}
	}

	m := numberHintRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, false
	}
	n, parseErr := strconv.ParseFloat(m[1], 64)
	if parseErr != nil || n <= 0 {
		return 0, false
	}
	// A bare number means seconds: that is what the Retry-After header carries.
	unit := time.Second
	switch strings.ToLower(m[2]) {
	case "ms", "millisecond", "milliseconds":
		unit = time.Millisecond
	case "m", "min", "mins", "minute", "minutes":
		unit = time.Minute
	}
	return time.Duration(n * float64(unit)), true
}

// Delay returns how long to wait before the given retry attempt (0-based).
//
// A server-supplied hint wins over the configured schedule, since the server
// knows when its window reopens and we do not. An explicit cfg.Delays schedule
// wins over the exponential backoff. All three are clamped to cfg.MaxDelay so a
// provider cannot park a turn indefinitely.
func Delay(cfg Config, attempt int, err error) time.Duration {
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultConfig().MaxDelay
	}

	if hinted, ok := ServerDelay(err); ok {
		return min(hinted, maxDelay)
	}

	if n := len(cfg.Delays); n > 0 {
		idx := max(0, min(attempt, n-1))
		return min(cfg.Delays[idx], maxDelay)
	}

	initial := cfg.InitialDelay
	if initial <= 0 {
		initial = DefaultConfig().InitialDelay
	}
	backoff := float64(initial) * math.Pow(2, float64(attempt))
	if backoff > float64(maxDelay) {
		return maxDelay
	}
	return time.Duration(backoff)
}
