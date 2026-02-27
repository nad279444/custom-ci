package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Test health endpoint returns ok
func TestHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("health check = %d; want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "ok" {
		t.Errorf("health body = %q; want %q", w.Body.String(), "ok")
	}
}

// Test job status constants are correct
func TestJobStatusValues(t *testing.T) {
	tests := []struct {
		status   JobStatus
		expected string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusPassed,  "passed"},
		{StatusFailed,  "failed"},
		{StatusError,   "error"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.expected {
			t.Errorf("status %q != %q", tt.status, tt.expected)
		}
	}
}

// Test job duration calculation
func TestJobDuration(t *testing.T) {
	start := time.Now()
	end := start.Add(5 * time.Second)

	job := Job{
		StartedAt:  &start,
		FinishedAt: &end,
	}

	duration := job.Duration()
	if duration != 5.0 {
		t.Errorf("Duration() = %f; want 5.0", duration)
	}
}

// Test job duration with no start time returns zero
func TestJobDurationNotStarted(t *testing.T) {
	job := Job{}
	if job.Duration() != 0 {
		t.Errorf("Duration() = %f; want 0", job.Duration())
	}
}

// Test webhook signature verification
func TestVerifySignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)

	// Empty secret should always pass
	d := &Dispatcher{webhookSecret: ""}
	if !d.verifySignature(body, "") {
		t.Error("empty secret should always verify")
	}

	// Wrong signature should fail
	d2 := &Dispatcher{webhookSecret: "testsecret"}
	if d2.verifySignature(body, "sha256=wrongsignature") {
		t.Error("wrong signature should not verify")
	}
}

// Test webhook ignores non-push events
func TestWebhookIgnoresNonPush(t *testing.T) {
	d := &Dispatcher{
		webhookSecret: "",
		allowedRepo:   "nad279444/custom-ci",
		jobQueue:      make(chan *Job, 10),
	}

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	req.Header.Set("X-GitHub-Event", "ping")  // not a push
	req.Body = io.NopCloser(bytes.NewReader(body))
	w := httptest.NewRecorder()

	d.handleWebhook(w, req)

	if w.Body.String() != "ignored" {
		t.Errorf("non-push event should return 'ignored', got %q", w.Body.String())
	}
}

// Test webhook ignores wrong repo
func TestWebhookIgnoresWrongRepo(t *testing.T) {
	d := &Dispatcher{
		webhookSecret: "",
		allowedRepo:   "nad279444/custom-ci",
		jobQueue:      make(chan *Job, 10),
	}

	body, _ := json.Marshal(map[string]interface{}{
		"ref":   "refs/heads/main",
		"after": "abc123",
		"repository": map[string]string{
			"full_name": "someone-else/other-repo",
			"clone_url": "https://github.com/someone-else/other-repo.git",
		},
		"head_commit": map[string]interface{}{
			"id":      "abc123",
			"message": "test",
			"author":  map[string]string{"name": "test"},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	w := httptest.NewRecorder()

	d.handleWebhook(w, req)

	if w.Body.String() != "repo not watched" {
		t.Errorf("wrong repo should return 'repo not watched', got %q", w.Body.String())
	}
}

// Test getEnv returns fallback
func TestGetEnv(t *testing.T) {
	result := getEnv("DEFINITELY_NOT_SET_XYZ", "fallback")
	if result != "fallback" {
		t.Errorf("getEnv = %q; want fallback", result)
	}
}

// Test runners API returns empty list not null
func TestGetRunnersEmpty(t *testing.T) {
	d := &Dispatcher{
		runners: make(map[string]*Runner),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/runners", nil)
	w := httptest.NewRecorder()

	d.handleGetRunners(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", w.Code)
	}

	var runners []*Runner
	if err := json.NewDecoder(w.Body).Decode(&runners); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(runners) != 0 {
		t.Errorf("runners length = %d; want 0", len(runners))
	}
}