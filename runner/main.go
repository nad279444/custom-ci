package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type JobStatus string

const (
	StatusPassed JobStatus = "passed"
	StatusFailed JobStatus = "failed"
	StatusError  JobStatus = "error"
)

// ── Runner ────────────────────────────────────────────────────────────────────

type Runner struct {
	id            string
	address       string
	dispatcherURL string
	workDir       string
	busy          bool
	mu            sync.Mutex
}

func NewRunner(dispatcherURL, address, workDir string) *Runner {
	return &Runner{
		id:            uuid.New().String(),
		address:       address,
		dispatcherURL: dispatcherURL,
		workDir:       workDir,
	}
}

// ── Registration & Heartbeat ──────────────────────────────────────────────────

func (r *Runner) register() error {
	payload, _ := json.Marshal(map[string]string{
		"id":      r.id,
		"address": r.address,
	})

	resp, err := http.Post(
		r.dispatcherURL+"/runner/register",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register: status %d", resp.StatusCode)
	}

	log.Printf("✅ registered with dispatcher as %s", r.id[:8])
	return nil
}

func (r *Runner) heartbeatLoop() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		payload, _ := json.Marshal(map[string]string{"id": r.id})
		resp, err := http.Post(
			r.dispatcherURL+"/runner/heartbeat",
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			log.Printf("💔 heartbeat failed: %v — re-registering", err)
			for {
				if err := r.register(); err == nil {
					break
				}
				time.Sleep(5 * time.Second)
			}
			continue
		}
		resp.Body.Close()
	}
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

func (r *Runner) handleRun(w http.ResponseWriter, req *http.Request) {
	var job struct {
		JobID     string `json:"job_id"`
		CommitSHA string `json:"commit_sha"`
		RepoURL   string `json:"repo_url"`
		RepoName  string `json:"repo_name"`
	}

	if err := json.NewDecoder(req.Body).Decode(&job); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	if r.busy {
		r.mu.Unlock()
		http.Error(w, "busy", http.StatusConflict)
		return
	}
	r.busy = true
	r.mu.Unlock()

	// Acknowledge immediately so dispatcher doesn't time out
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})

	// Run tests in background
	go func() {
		defer func() {
			r.mu.Lock()
			r.busy = false
			r.mu.Unlock()
		}()

		status, output := r.runTests(job.JobID, job.CommitSHA, job.RepoURL)
		r.reportResult(job.JobID, status, output)
	}()
}

func (r *Runner) handleHealth(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	busy := r.busy
	r.mu.Unlock()

	status := "idle"
	if busy {
		status = "busy"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": status,
		"id":     r.id,
	})
}

// ── Test Execution ────────────────────────────────────────────────────────────

func (r *Runner) runTests(jobID, commitSHA, repoURL string) (JobStatus, string) {
	var out strings.Builder
	shortSHA := commitSHA[:8]

	log.Printf("🧪 [%s] starting tests for commit %s", jobID[:8], shortSHA)

	// Unique workspace per job — cleaned up after
	workspace := filepath.Join(r.workDir, jobID)
	defer os.RemoveAll(workspace)

	if err := os.MkdirAll(workspace, 0755); err != nil {
		return StatusError, fmt.Sprintf("failed to create workspace: %v", err)
	}

	// ── Step 1: Clone ──────────────────────────────────────────────────────────
	fmt.Fprintf(&out, "╔══════════════════════════════════════╗\n")
	fmt.Fprintf(&out, "║  custom-ci test runner               ║\n")
	fmt.Fprintf(&out, "╚══════════════════════════════════════╝\n\n")
	fmt.Fprintf(&out, "📦 Cloning %s\n", repoURL)
	fmt.Fprintf(&out, "📌 Commit: %s\n\n", shortSHA)

	cloneOut, err := r.runCmd(workspace, 30*time.Minute, "git", "clone", repoURL, "repo")
	out.WriteString(cloneOut)
	if err != nil {
		fmt.Fprintf(&out, "\n❌ Clone failed: %v\n", err)
		return StatusError, out.String()
	}

	repoPath := filepath.Join(workspace, "repo")

	// ── Step 2: Checkout commit ────────────────────────────────────────────────
	fmt.Fprintf(&out, "\n── Checking out %s ──\n", shortSHA)
	checkoutOut, err := r.runCmd(repoPath, 5*time.Minute, "git", "checkout", commitSHA)
	out.WriteString(checkoutOut)
	if err != nil {
		fmt.Fprintf(&out, "\n❌ Checkout failed: %v\n", err)
		return StatusError, out.String()
	}

	// ── Step 3: Download dependencies ─────────────────────────────────────────
	fmt.Fprintf(&out, "\n── Downloading dependencies ──\n")
	modOut, err := r.runCmd(repoPath, 5*time.Minute, "go", "mod", "download")
	out.WriteString(modOut)
	if err != nil {
		fmt.Fprintf(&out, "⚠️  go mod download warning: %v\n", err)
	}

	// ── Step 4: go vet ────────────────────────────────────────────────────────
	fmt.Fprintf(&out, "\n── Running go vet ──\n")
	vetOut, vetErr := r.runCmd(repoPath, 2*time.Minute, "go", "vet", "./...")
	out.WriteString(vetOut)
	if vetErr != nil {
		fmt.Fprintf(&out, "⚠️  go vet found issues: %v\n", vetErr)
	} else {
		fmt.Fprintf(&out, "✅ go vet passed\n")
	}

	// ── Step 5: go test ───────────────────────────────────────────────────────
	fmt.Fprintf(&out, "\n── Running go test ./... ──\n\n")
	testOut, testErr := r.runCmd(repoPath, 10*time.Minute,
		"go", "test", "-v", "-count=1", "-timeout=5m", "./...")
	out.WriteString(testOut)

	if testErr != nil {
		fmt.Fprintf(&out, "\n❌ TESTS FAILED\n")
		return StatusFailed, out.String()
	}

	fmt.Fprintf(&out, "\n✅ ALL TESTS PASSED\n")
	return StatusPassed, out.String()
}

func (r *Runner) runCmd(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(timeout):
		cmd.Process.Kill()
		return out.String(), fmt.Errorf("timed out after %v", timeout)
	}
}

// ── Report Result ─────────────────────────────────────────────────────────────

func (r *Runner) reportResult(jobID string, status JobStatus, output string) {
	payload, _ := json.Marshal(map[string]string{
		"job_id":    jobID,
		"runner_id": r.id,
		"status":    string(status),
		"output":    output,
	})

	for attempt := 1; attempt <= 5; attempt++ {
		resp, err := http.Post(
			r.dispatcherURL+"/runner/result",
			"application/json",
			bytes.NewReader(payload),
		)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			log.Printf("📬 [%s] result reported: %s", jobID[:8], status)
			return
		}
		log.Printf("📭 report attempt %d failed — retrying in %ds", attempt, attempt*2)
		time.Sleep(time.Duration(attempt*2) * time.Second)
	}

	log.Printf("⚠️  [%s] failed to report result after 5 attempts", jobID[:8])
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	dispatcherURL := getEnv("DISPATCHER_URL", "http://dispatcher:8080")
	port          := getEnv("PORT",           "8090")
	workDir       := getEnv("WORK_DIR",       "/tmp/ci-runner")
	selfAddress   := getEnv("SELF_ADDRESS",   "runner:8090")

	log.Printf("🤖 runner starting on :%s", port)
	log.Printf("🔗 dispatcher: %s", dispatcherURL)

	if err := os.MkdirAll(workDir, 0755); err != nil {
		log.Fatalf("create workdir: %v", err)
	}

	runner := NewRunner(dispatcherURL, selfAddress, workDir)

	// Keep trying to register until dispatcher is ready
	for {
		if err := runner.register(); err != nil {
			log.Printf("⏳ waiting for dispatcher: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		break
	}

	go runner.heartbeatLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/run",    runner.handleRun)
	mux.HandleFunc("/health", runner.handleHealth)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
