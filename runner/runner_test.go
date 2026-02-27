package main

import (
	"testing"
	"os"
)

// Test runner creates work directory
func TestRunnerWorkDir(t *testing.T) {
	workDir := "/tmp/test-runner-workdir"
	defer os.RemoveAll(workDir)

	runner := NewRunner(
		"http://localhost:8080",
		"runner:8090",
		workDir,
	)

	if runner.id == "" {
		t.Error("runner ID should not be empty")
	}

	if runner.dispatcherURL != "http://localhost:8080" {
		t.Errorf("dispatcherURL = %q; want %q",
			runner.dispatcherURL, "http://localhost:8080")
	}

	if runner.address != "runner:8090" {
		t.Errorf("address = %q; want %q",
			runner.address, "runner:8090")
	}

	if runner.busy {
		t.Error("new runner should not be busy")
	}
}

// Test runner ID is unique per instance
func TestRunnerUniqueID(t *testing.T) {
	r1 := NewRunner("http://localhost:8080", "runner1:8090", "/tmp")
	r2 := NewRunner("http://localhost:8080", "runner2:8090", "/tmp")

	if r1.id == r2.id {
		t.Error("each runner should have a unique ID")
	}
}

// Test job status values
func TestRunnerStatusValues(t *testing.T) {
	if string(StatusPassed) != "passed" {
		t.Errorf("StatusPassed = %q; want passed", StatusPassed)
	}
	if string(StatusFailed) != "failed" {
		t.Errorf("StatusFailed = %q; want failed", StatusFailed)
	}
	if string(StatusError) != "error" {
		t.Errorf("StatusError = %q; want error", StatusError)
	}
}

// Test getEnv returns fallback when env not set
func TestGetEnvFallback(t *testing.T) {
	result := getEnv("DEFINITELY_NOT_SET_VAR_XYZ", "fallback")
	if result != "fallback" {
		t.Errorf("getEnv fallback = %q; want fallback", result)
	}
}

// Test getEnv returns value when env is set
func TestGetEnvSet(t *testing.T) {
	os.Setenv("TEST_CI_VAR", "testvalue")
	defer os.Unsetenv("TEST_CI_VAR")

	result := getEnv("TEST_CI_VAR", "fallback")
	if result != "testvalue" {
		t.Errorf("getEnv = %q; want testvalue", result)
	}
}