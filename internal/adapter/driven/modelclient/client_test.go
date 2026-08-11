package modelclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"resumesearch/internal/adapter/driven/modelclient"
	"resumesearch/internal/constants"
	"resumesearch/internal/domain"
)

func TestNew_TrimsTrailingSlashFromURLs(t *testing.T) {
	// Docker Model Runner's injected LLM_URL/EMBED_URL was verified
	// empirically to end in a slash ("http://.../v1/"); New must not
	// double that up against the literal "/embeddings" suffix.
	cases := []struct {
		name    string
		baseURL func(serverURL string) string
	}{
		{name: "no trailing slash", baseURL: func(u string) string { return u }},
		{name: "one trailing slash", baseURL: func(u string) string { return u + "/" }},
		{name: "trailing slash with a path segment, like Docker Model Runner's /v1/", baseURL: func(u string) string { return u + "/v1/" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gotPath = req.URL.Path
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"embedding": []float32{0.1}}},
				})
			}))
			defer server.Close()

			base := tc.baseURL(server.URL)
			c := modelclient.New(base, "llm-model", "", base, "embed-model")
			if _, err := c.Embed(t.Context(), "text"); err != nil {
				t.Fatalf("Embed failed: %v", err)
			}

			if strings.Contains(gotPath, "//") {
				t.Errorf("request path contains a double slash: %q", gotPath)
			}
			wantSuffix := "/embeddings"
			if !strings.HasSuffix(gotPath, wantSuffix) {
				t.Errorf("request path %q does not end with %q", gotPath, wantSuffix)
			}
		})
	}
}

func TestEmbed(t *testing.T) {
	cases := []struct {
		name       string
		handler    http.HandlerFunc
		wantErr    bool
		wantVector []float32
	}{
		{
			name: "success returns embedding vector",
			handler: func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/embeddings" {
					t.Errorf("unexpected path: %s", req.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []map[string]any{{"embedding": []float32{0.1, 0.2, 0.3}}},
				})
			},
			wantVector: []float32{0.1, 0.2, 0.3},
		},
		{
			name: "non-200 status is an error, not a decode panic",
			handler: func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal error"))
			},
			wantErr: true,
		},
		{
			name: "empty data array is an error",
			handler: func(w http.ResponseWriter, req *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()

			c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
			vec, err := c.Embed(t.Context(), "some resume text")

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Embed failed: %v", err)
			}
			if len(vec) != len(tc.wantVector) || vec[0] != tc.wantVector[0] {
				t.Errorf("got %v, want %v", vec, tc.wantVector)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	cases := []struct {
		name        string
		responses   []string // one chat-completion "content" value per call, in order
		wantErr     bool
		wantSkills  int
		wantAttempt int
	}{
		{
			name:        "clean JSON succeeds on first attempt",
			responses:   []string{`{"skills":["go","postgres"],"years_experience":5,"location":"Remote"}`},
			wantSkills:  2,
			wantAttempt: 1,
		},
		{
			name:        "markdown-fenced JSON is stripped and parsed",
			responses:   []string{"```json\n" + `{"skills":["go"],"years_experience":3,"location":"Remote"}` + "\n```"},
			wantSkills:  1,
			wantAttempt: 1,
		},
		{
			name:        "invalid JSON then valid JSON retries once and succeeds",
			responses:   []string{"not valid json at all", `{"skills":["go","postgres"],"years_experience":5,"location":"Remote"}`},
			wantSkills:  2,
			wantAttempt: 2,
		},
		{
			name:        "invalid JSON on every attempt exhausts retries and fails",
			responses:   []string{"still not json", "still not json", "still not json"},
			wantErr:     true,
			wantAttempt: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempt := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				content := tc.responses[min(attempt, len(tc.responses)-1)]
				attempt++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]any{"content": content}}},
				})
			}))
			defer server.Close()

			c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
			fields, err := c.Extract(t.Context(), "resume text with go and postgres, 5 years")

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error after exhausting retries, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("Extract failed: %v", err)
				}
				if len(fields.Skills) != tc.wantSkills {
					t.Errorf("got %d skills, want %d: %+v", len(fields.Skills), tc.wantSkills, fields)
				}
			}
			if attempt != tc.wantAttempt {
				t.Errorf("got %d attempts, want %d", attempt, tc.wantAttempt)
			}
		})
	}
}

// TestExtract_BacksOffBetweenAttempts locks in the ctx-aware backoff added
// alongside the per-attempt timeout: a retry against a model that just
// failed must not fire immediately with zero delay.
func TestExtract_BacksOffBetweenAttempts(t *testing.T) {
	cases := []struct {
		name        string
		responses   []string
		wantMinWait time.Duration // lower bound on elapsed wall-clock time
	}{
		{
			name:        "succeeds first try — no backoff incurred",
			responses:   []string{`{"skills":[],"years_experience":0,"location":""}`},
			wantMinWait: 0,
		},
		{
			name:        "one retry incurs exactly one backoff period",
			responses:   []string{"not json", `{"skills":[],"years_experience":0,"location":""}`},
			wantMinWait: constants.ExtractionRetryBackoff,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempt := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				content := tc.responses[min(attempt, len(tc.responses)-1)]
				attempt++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]any{"content": content}}},
				})
			}))
			defer server.Close()

			c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
			start := time.Now()
			if _, err := c.Extract(t.Context(), "text"); err != nil {
				t.Fatalf("Extract failed: %v", err)
			}
			if elapsed := time.Since(start); elapsed < tc.wantMinWait {
				t.Errorf("elapsed %v, want at least %v", elapsed, tc.wantMinWait)
			}
		})
	}
}

// TestExtract_CanceledContextStopsRetryingDuringBackoff ensures the backoff
// wait is itself ctx-aware: canceling the caller's context must not force
// Extract to wait out a full backoff period before returning.
func TestExtract_CanceledContextStopsRetryingDuringBackoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "not json"}}},
		})
	}))
	defer server.Close()

	c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.Extract(ctx, "text")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if elapsed >= constants.ExtractionRetryBackoff {
		t.Errorf("Extract took %v to return after cancellation, want well under the %v backoff", elapsed, constants.ExtractionRetryBackoff)
	}
}

// TestWarmUp covers WarmUp's one remaining call (embed) and, critically,
// asserts /chat/completions is never hit — chat/extraction now runs
// against a hosted API with no cold-start to warm, so WarmUp must not touch
// it at all anymore.
func TestWarmUp(t *testing.T) {
	cases := []struct {
		name        string
		embedStatus int
		wantErr     bool
	}{
		{
			name:        "embed endpoint healthy succeeds",
			embedStatus: http.StatusOK,
		},
		{
			name:        "embed endpoint failing is an error",
			embedStatus: http.StatusInternalServerError,
			wantErr:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chatCalls := 0
			embedCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/chat/completions":
					chatCalls++
					w.WriteHeader(http.StatusInternalServerError)
				case "/embeddings":
					embedCalls++
					if tc.embedStatus != http.StatusOK {
						w.WriteHeader(tc.embedStatus)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": []map[string]any{{"embedding": []float32{0.1}}},
					})
				}
			}))
			defer server.Close()

			c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
			err := c.WarmUp(t.Context())

			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("WarmUp failed: %v", err)
			}
			if embedCalls != 1 {
				t.Errorf("got %d calls to /embeddings, want 1", embedCalls)
			}
			if chatCalls != 0 {
				t.Errorf("got %d calls to /chat/completions, want 0 — WarmUp must not touch the hosted chat API", chatCalls)
			}
		})
	}
}

func TestExtract_YearsExperienceFieldRoundTrips(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":7.5,"location":""}`}}},
		})
	}))
	defer server.Close()

	c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
	fields, err := c.Extract(t.Context(), "text")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	want := domain.ExtractedFields{Skills: []string{}, YearsExperience: 7.5, Location: ""}
	if fields.YearsExperience != want.YearsExperience {
		t.Errorf("got years_experience %v, want %v", fields.YearsExperience, want.YearsExperience)
	}
}

// TestExtract_SendsAuthHeaderWhenKeyProvided locks in that the Authorization
// header is sent only when an API key is configured — DMR needs none, a
// hosted provider does.
func TestExtract_SendsAuthHeaderWhenKeyProvided(t *testing.T) {
	cases := []struct {
		name     string
		apiKey   string
		wantAuth string // expected header value; "" means expect the header absent
	}{
		{name: "key provided sends bearer header", apiKey: "sk-test-123", wantAuth: "Bearer sk-test-123"},
		{name: "empty key sends no auth header", apiKey: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gotAuth = req.Header.Get("Authorization")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":0,"location":""}`}}},
				})
			}))
			defer server.Close()

			c := modelclient.New(server.URL, "llm-model", tc.apiKey, server.URL, "embed-model")
			if _, err := c.Extract(t.Context(), "text"); err != nil {
				t.Fatalf("Extract failed: %v", err)
			}

			if gotAuth != tc.wantAuth {
				t.Errorf("got Authorization header %q, want %q", gotAuth, tc.wantAuth)
			}
		})
	}
}

// TestExtract_SendsMaxTokens locks in that chat completion requests cap
// completion length — a billed hosted API means an unbounded completion
// is an unbounded bill, unlike free DMR.
func TestExtract_SendsMaxTokens(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":0,"location":""}`}}},
		})
	}))
	defer server.Close()

	c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
	if _, err := c.Extract(t.Context(), "text"); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	maxTokens, ok := gotBody["max_tokens"]
	if !ok {
		t.Fatal("expected max_tokens in request body, got none")
	}
	if got := int(maxTokens.(float64)); got != constants.MaxExtractionCompletionTokens {
		t.Errorf("got max_tokens %d, want %d", got, constants.MaxExtractionCompletionTokens)
	}
}

// TestExtract_UsageFieldDoesNotBreakParsing confirms a response carrying a
// provider's usage object (prompt_tokens/completion_tokens/total_tokens,
// which chatCompletion now logs for cost visibility) still decodes cleanly.
func TestExtract_UsageFieldDoesNotBreakParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":["go"],"years_experience":1,"location":""}`}}},
			"usage":   map[string]any{"prompt_tokens": 42, "completion_tokens": 7, "total_tokens": 49},
		})
	}))
	defer server.Close()

	c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
	fields, err := c.Extract(t.Context(), "text")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if len(fields.Skills) != 1 {
		t.Errorf("got %d skills, want 1: %+v", len(fields.Skills), fields)
	}
}

// TestExtract_RetriesAfterRateLimitHonoringRetryAfter locks in that a 429
// with a Retry-After hint drives the retry loop's backoff instead of the
// fixed constants.ExtractionRetryBackoff — DMR never rate-limited, a
// billed hosted API does, and honoring its hint avoids hammering it with
// the default 500ms pause.
func TestExtract_RetriesAfterRateLimitHonoringRetryAfter(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		attempt++
		if attempt == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":0,"location":""}`}}},
		})
	}))
	defer server.Close()

	c := modelclient.New(server.URL, "llm-model", "", server.URL, "embed-model")
	start := time.Now()
	if _, err := c.Extract(t.Context(), "text"); err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < time.Second {
		t.Errorf("elapsed %v, want at least the 1s Retry-After hint, not the fixed %v backoff", elapsed, constants.ExtractionRetryBackoff)
	}
	if attempt != 2 {
		t.Errorf("got %d attempts, want 2", attempt)
	}
}
