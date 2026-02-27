package main

import (
	"testing"
	"time"
)

// Test job duration formatting
func TestJobDurationFormatting(t *testing.T) {
	tests := []struct {
		seconds  float64
		expected string
	}{
		{30,   "30.0s"},
		{90,   "1m 30s"},
		{3661, "61m 1s"},
	}

	for _, tt := range tests {
		start := time.Now()
		end := start.Add(time.Duration(tt.seconds) * time.Second)

		job := Job{
			StartedAt:  &start,
			FinishedAt: &end,
		}

		got := job.Duration()
		if got != tt.expected {
			t.Errorf("Duration(%gs) = %q; want %q",
				tt.seconds, got, tt.expected)
		}
	}
}

// Test job with no times shows dash
func TestJobDurationNoTimes(t *testing.T) {
	job := Job{}
	if job.Duration() != "—" {
		t.Errorf("Duration() = %q; want —", job.Duration())
	}
}

// Test short SHA
func TestJobShortSHA(t *testing.T) {
	job := Job{CommitSHA: "abc123def456"}
	if job.ShortSHA() != "abc123de" {
		t.Errorf("ShortSHA() = %q; want abc123de", job.ShortSHA())
	}
}

// Test short SHA with short commit
func TestJobShortSHATooShort(t *testing.T) {
	job := Job{CommitSHA: "abc"}
	if job.ShortSHA() != "abc" {
		t.Errorf("ShortSHA() = %q; want abc", job.ShortSHA())
	}
}

// Test runner display name
func TestJobShortRunner(t *testing.T) {
	job := Job{RunnerID: "abcdef123456"}
	if job.ShortRunner() != "runner-abcdef12" {
		t.Errorf("ShortRunner() = %q; want runner-abcdef12", job.ShortRunner())
	}
}

// Test empty runner shows dash
func TestJobShortRunnerEmpty(t *testing.T) {
	job := Job{RunnerID: ""}
	if job.ShortRunner() != "—" {
		t.Errorf("ShortRunner() = %q; want —", job.ShortRunner())
	}
}

// Test status icon mapping
func TestStatusIcon(t *testing.T) {
	tests := []struct {
		status   JobStatus
		expected string
	}{
		{"passed",  "✅"},
		{"failed",  "❌"},
		{"running", "🔄"},
		{"pending", "⏳"},
		{"error",   "⚠️"},
	}

	for _, tt := range tests {
		got := statusIcon(tt.status)
		if got != tt.expected {
			t.Errorf("statusIcon(%q) = %q; want %q",
				tt.status, got, tt.expected)
		}
	}
}

// Test stats calculation
func TestCalcStats(t *testing.T) {
	jobs := []Job{
		{Status: "passed"},
		{Status: "passed"},
		{Status: "failed"},
		{Status: "running"},
		{Status: "pending"},
	}

	stats := calcStats(jobs)

	if stats.Total != 5 {
		t.Errorf("Total = %d; want 5", stats.Total)
	}
	if stats.Passed != 2 {
		t.Errorf("Passed = %d; want 2", stats.Passed)
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d; want 1", stats.Failed)
	}
	if stats.Running != 1 {
		t.Errorf("Running = %d; want 1", stats.Running)
	}
	if stats.PassRate != "67%" {
		t.Errorf("PassRate = %q; want 67%%", stats.PassRate)
	}
}

// Test stats with no jobs
func TestCalcStatsEmpty(t *testing.T) {
	stats := calcStats([]Job{})

	if stats.Total != 0 {
		t.Errorf("Total = %d; want 0", stats.Total)
	}
	if stats.PassRate != "—" {
		t.Errorf("PassRate = %q; want —", stats.PassRate)
	}
}

// Test timeAgo formatting
func TestTimeAgo(t *testing.T) {
	now := time.Now()

	if timeAgo(now.Add(-30*time.Second)) != "30s ago" {
		t.Error("30 seconds ago failed")
	}
	if timeAgo(now.Add(-5*time.Minute)) != "5m ago" {
		t.Error("5 minutes ago failed")
	}
	if timeAgo(now.Add(-3*time.Hour)) != "3h ago" {
		t.Error("3 hours ago failed")
	}
	if timeAgo(now.Add(-48*time.Hour)) != "2d ago" {
		t.Error("2 days ago failed")
	}
}