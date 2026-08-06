package modelclient_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"resumesearch/internal/adapter/driven/modelclient"
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
			c := modelclient.New(base, "llm-model", base, "embed-model")
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

			c := modelclient.New(server.URL, "llm-model", server.URL, "embed-model")
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

			c := modelclient.New(server.URL, "llm-model", server.URL, "embed-model")
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

func TestExtract_YearsExperienceFieldRoundTrips(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":7.5,"location":""}`}}},
		})
	}))
	defer server.Close()

	c := modelclient.New(server.URL, "llm-model", server.URL, "embed-model")
	fields, err := c.Extract(t.Context(), "text")
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	want := domain.ExtractedFields{Skills: []string{}, YearsExperience: 7.5, Location: ""}
	if fields.YearsExperience != want.YearsExperience {
		t.Errorf("got years_experience %v, want %v", fields.YearsExperience, want.YearsExperience)
	}
}
