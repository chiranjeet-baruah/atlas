package modelclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/exp/slog"

	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
)

// Client is the single OpenAI-compatible HTTP client used for both LLM
// extraction and embeddings. Parameterized by base URL + model name per
// role. Embeddings still run on Docker Model Runner; chat/extraction runs
// against a hosted API (e.g. Groq) authenticated via llmAPIKey — both speak
// the same OpenAI-compatible chat/completions and embeddings shape, so the
// backend for either role is an env-var change, not a code change.
type Client struct {
	llmURL     string
	llmModel   string
	llmAPIKey  string
	embedURL   string
	embedModel string
	httpClient *http.Client
}

// New builds a Client. llmURL/embedURL may or may not have a trailing
// slash — Docker Model Runner's injected `EMBED_URL` (verified empirically:
// "http://model-runner.docker.internal/v1/") does include one, and a bare
// trailing-slash concatenation would otherwise produce a double slash
// before "chat/completions"/"embeddings". llmAPIKey is sent as a Bearer
// token on chat requests only; pass "" for backends (like DMR) that need
// no auth.
func New(llmURL, llmModel, llmAPIKey, embedURL, embedModel string) *Client {
	return &Client{
		llmURL:     strings.TrimSuffix(llmURL, "/"),
		llmModel:   llmModel,
		llmAPIKey:  llmAPIKey,
		embedURL:   strings.TrimSuffix(embedURL, "/"),
		embedModel: embedModel,
		httpClient: &http.Client{},
	}
}

// extractionPrompt's years_experience definition is stated verbatim per
// project convention, so extraction is consistent across every resume and
// every retry.
const extractionPrompt = `Extract structured fields from the resume text below as a single JSON object with exactly these keys:
- "skills": array of strings, technical/professional skills mentioned
- "years_experience": number, TOTAL professional experience computed from the earliest job start date to the latest job end date (or today if the role is current)
- "location": string, candidate's location if mentioned, else empty string

Return ONLY the JSON object, no other text.

Resume text:
%s`

// Extract retries up to constants.MaxExtractionRetries times. Each attempt
// gets its own constants.LLMAttemptTimeout budget rather than sharing one
// deadline across all attempts — otherwise a slow first attempt that blocks
// to the deadline leaves nothing for attempts 2 and 3, so the effective
// retry budget silently collapses to whatever's left of the caller's
// deadline instead of 3 independent chances.
func (c *Client) Extract(ctx context.Context, text string) (domain.ExtractedFields, error) {
	var lastErr error
	for attempt := 1; attempt <= constants.MaxExtractionRetries; attempt++ {
		fields, err := c.extractOnce(ctx, text, attempt)
		if err == nil {
			return fields, nil
		}
		lastErr = err

		if attempt < constants.MaxExtractionRetries {
			delay := constants.ExtractionRetryBackoff
			var rle *rateLimitError
			if errors.As(err, &rle) && rle.retryAfter > 0 {
				delay = rle.retryAfter
			}
			select {
			case <-ctx.Done():
				return domain.ExtractedFields{}, fmt.Errorf("extraction failed after %d attempts: %w", attempt, lastErr)
			case <-time.After(delay):
			}
		}
	}
	return domain.ExtractedFields{}, fmt.Errorf("extraction failed after %d attempts: %w", constants.MaxExtractionRetries, lastErr)
}

func (c *Client) extractOnce(ctx context.Context, text string, attempt int) (domain.ExtractedFields, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, constants.LLMAttemptTimeout)
	defer cancel()

	if len(text) > constants.MaxExtractionTextChars {
		text = text[:constants.MaxExtractionTextChars]
	}

	content, err := c.chatCompletion(attemptCtx, fmt.Sprintf(extractionPrompt, text))
	if err != nil {
		return domain.ExtractedFields{}, err
	}
	var fields domain.ExtractedFields
	if err := json.Unmarshal([]byte(extractJSONObject(content)), &fields); err != nil {
		return domain.ExtractedFields{}, fmt.Errorf("invalid JSON from model (attempt %d): %w", attempt, err)
	}
	return fields, nil
}

// extractJSONObject strips common local-model formatting noise — markdown
// code fences and any leading/trailing prose — down to the first balanced
// {...} object, so `json.Unmarshal` sees clean JSON instead of failing the
// whole extraction attempt over a fence.
func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	start := strings.IndexByte(content, '{')
	if start == -1 {
		return content
	}

	depth := 0
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return content[start : i+1]
			}
		}
	}
	return content[start:]
}

// rateLimitError is returned when a chat completion hits a 429, carrying
// the provider's Retry-After hint (0 if absent/unparseable) so Extract's
// retry loop can honor it instead of the default fixed backoff — DMR never
// rate-limited, but a billed hosted API does.
type rateLimitError struct {
	retryAfter time.Duration
	status     int
	body       string
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("chat completion rate limited (status %d): %s", e.status, e.body)
}

// parseRetryAfter parses a Retry-After header value, trying integer-seconds
// form (what OpenAI-compatible providers send) then the HTTP-date form.
// Returns 0 if empty or unparseable, signaling "no hint" to the caller.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

func (c *Client) chatCompletion(ctx context.Context, prompt string) (string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model": c.llmModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": constants.MaxExtractionCompletionTokens,
	})
	if err != nil {
		return "", fmt.Errorf("marshal chat completion request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.llmURL+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build chat completion request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.llmAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.llmAPIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat completion request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read chat completion response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &rateLimitError{
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			status:     resp.StatusCode,
			body:       truncate(body, 500),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat completion returned status %d: %s", resp.StatusCode, truncate(body, 500))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode chat completion response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("no choices in chat completion response")
	}
	slog.InfoContext(ctx, "chat completion usage",
		"prompt_tokens", parsed.Usage.PromptTokens,
		"completion_tokens", parsed.Usage.CompletionTokens,
		"total_tokens", parsed.Usage.TotalTokens,
	)
	return parsed.Choices[0].Message.Content, nil
}

// WarmUp issues one trivial embed call, forcing Docker Model Runner to load
// the embedding model if it's currently cold/evicted. Chat/extraction runs
// against a hosted API with no cold-start/eviction to warm, so there is no
// LLM half to this anymore. It's a latency optimization only: a real
// resume request landing on a cold embed model still succeeds via its own
// timeout budget, so callers should log a WarmUp error rather than treat it
// as fatal.
func (c *Client) WarmUp(ctx context.Context) error {
	embedCtx, cancel := context.WithTimeout(ctx, constants.EmbedAttemptTimeout)
	defer cancel()
	if _, err := c.Embed(embedCtx, "warmup"); err != nil {
		return fmt.Errorf("warm up embed: %w", err)
	}
	return nil
}

func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model": c.embedModel,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal embeddings request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.embedURL+"/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("build embeddings request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings request returned status %d: %s", resp.StatusCode, truncate(body, 500))
	}

	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("no embedding data in response")
	}
	return parsed.Data[0].Embedding, nil
}

func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
