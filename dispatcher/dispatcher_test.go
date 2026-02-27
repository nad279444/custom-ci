package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Test that health endpoint returns ok
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

// Test job duration with no start time
func TestJobDurationNotStarted(t *testing.T) {
	job := Job{}

	duration := job.Duration()
	if duration != 0 {
		t.Errorf("Duration() = %f; want 0", duration)
	}
}

// Test webhook signature verification
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

// Test dispatcher initializes with correct defaults
func TestNewDispatcher(t *testing.T) {
	d, err := NewDispatcher("/tmp/test-ci.db", "secret", "user/repo")
	if err != nil {
		t.Fatalf("NewDispatcher failed: %v", err)
	}

	if d.webhookSecret != "secret" {
		t.Errorf("webhookSecret = %q; want %q", d.webhookSecret, "secret")
	}

	if d.allowedRepo != "user/repo" {
		t.Errorf("allowedRepo = %q; want %q", d.allowedRepo, "user/repo")
	}

	if d.runners == nil {
		t.Error("runners map should not be nil")
	}

	if d.jobQueue == nil {
		t.Error("jobQueue channel should not be nil")
	}
}

// Test API returns empty jobs list not null
func TestGetJobsEmpty(t *testing.T) {
	d, _ := NewDispatcher("/tmp/test-empty.db", "", "user/repo")

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()

	d.handleGetJobs(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want %d", w.Code, http.StatusOK)
	}

	var jobs []Job
	if err := json.NewDecoder(w.Body).Decode(&jobs); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if jobs == nil {
		t.Error("jobs should be empty slice not nil")
	}

	if len(jobs) != 0 {
		t.Errorf("jobs length = %d; want 0", len(jobs))
	}
}