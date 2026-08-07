package modelclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"resumesearch/internal/constants"
)

// deadlineCapturingTransport records the deadline of each outgoing
// request's context, then delegates to the real transport. Since this is a
// whitebox test (package modelclient), it can reach into Client's private
// httpClient field to install this — no exported seam is needed just for a
// test.
type deadlineCapturingTransport struct {
	deadlines []time.Time
}

func (t *deadlineCapturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, _ := req.Context().Deadline()
	t.deadlines = append(t.deadlines, deadline)
	return http.DefaultTransport.RoundTrip(req)
}

// TestExtractOnce_DerivesFreshDeadlinePerAttempt locks in the per-attempt
// timeout fix: extractOnce must wrap the caller's ctx in its own
// context.WithTimeout(ctx, constants.LLMAttemptTimeout) rather than reusing
// whatever deadline the caller passed in. Before this fix, all attempts
// shared one ctx/deadline, so a slow first attempt that blocked to the
// deadline left nothing for attempts 2 and 3.
func TestExtractOnce_DerivesFreshDeadlinePerAttempt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":0,"location":""}`}}},
		})
	}))
	defer server.Close()

	c := New(server.URL, "llm-model", server.URL, "embed-model")
	transport := &deadlineCapturingTransport{}
	c.httpClient.Transport = transport

	// Parent has no deadline of its own: if extractOnce didn't add one,
	// the captured request deadline would be zero.
	before := time.Now()
	if _, err := c.extractOnce(context.Background(), "text", 1); err != nil {
		t.Fatalf("extractOnce failed: %v", err)
	}
	after := time.Now()

	if len(transport.deadlines) != 1 {
		t.Fatalf("expected exactly 1 request, got %d", len(transport.deadlines))
	}
	deadline := transport.deadlines[0]
	if deadline.IsZero() {
		t.Fatal("expected the request's context to carry a deadline, got none")
	}
	wantMin := before.Add(constants.LLMAttemptTimeout - time.Second)
	wantMax := after.Add(constants.LLMAttemptTimeout + time.Second)
	if deadline.Before(wantMin) || deadline.After(wantMax) {
		t.Errorf("deadline %v not within a second of LLMAttemptTimeout (%v) from call time [%v, %v]", deadline, constants.LLMAttemptTimeout, wantMin, wantMax)
	}
}

// TestExtractOnce_ParentDeadlineDoesNotShrinkOrExtendAttemptBudget confirms
// the per-attempt deadline tracks constants.LLMAttemptTimeout specifically,
// not whatever budget happens to remain on the parent context — a parent
// with a much longer deadline must not hand that longer budget to a single
// attempt.
func TestExtractOnce_ParentDeadlineDoesNotShrinkOrExtendAttemptBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": `{"skills":[],"years_experience":0,"location":""}`}}},
		})
	}))
	defer server.Close()

	c := New(server.URL, "llm-model", server.URL, "embed-model")
	transport := &deadlineCapturingTransport{}
	c.httpClient.Transport = transport

	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	before := time.Now()
	if _, err := c.extractOnce(parent, "text", 1); err != nil {
		t.Fatalf("extractOnce failed: %v", err)
	}

	deadline := transport.deadlines[0]
	distance := deadline.Sub(before)
	if distance > constants.LLMAttemptTimeout+time.Second {
		t.Errorf("request deadline was %v out, want close to LLMAttemptTimeout (%v), not the parent's 1-hour budget", distance, constants.LLMAttemptTimeout)
	}
}
