package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// ── Types ─────────────────────────────────────────────────────────────────────

type JobStatus string

const (
	StatusPending JobStatus = "pending"
	StatusRunning JobStatus = "running"
	StatusPassed  JobStatus = "passed"
	StatusFailed  JobStatus = "failed"
	StatusError   JobStatus = "error"
)

type Job struct {
	ID         string    `json:"id"`
	CommitSHA  string    `json:"commit_sha"`
	RepoURL    string    `json:"repo_url"`
	RepoName   string    `json:"repo_name"`
	Branch     string    `json:"branch"`
	Author     string    `json:"author"`
	Message    string    `json:"message"`
	Status     JobStatus `json:"status"`
	Output     string    `json:"output"`
	RunnerID   string    `json:"runner_id"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Duration returns how long the job took in seconds
func (j *Job) Duration() float64 {
	if j.StartedAt == nil || j.FinishedAt == nil {
		return 0
	}
	return j.FinishedAt.Sub(*j.StartedAt).Seconds()
}

type Runner struct {
	ID         string    `json:"id"`
	Address    string    `json:"address"`
	Busy       bool      `json:"busy"`
	LastSeen   time.Time `json:"last_seen"`
	CurrentJob string    `json:"current_job,omitempty"`
}

type WebhookPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
	HeadCommit struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"head_commit"`
}

// ── Dispatcher ────────────────────────────────────────────────────────────────

type Dispatcher struct {
	db            *sql.DB
	runners       map[string]*Runner
	mu            sync.RWMutex
	jobQueue      chan *Job
	webhookSecret string
	allowedRepo   string
}

func NewDispatcher(dbPath, webhookSecret, allowedRepo string) (*Dispatcher, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	d := &Dispatcher{
		db:            db,
		runners:       make(map[string]*Runner),
		jobQueue:      make(chan *Job, 100),
		webhookSecret: webhookSecret,
		allowedRepo:   allowedRepo,
	}

	if err := d.initDB(); err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	return d, nil
}

func (d *Dispatcher) initDB() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS jobs (
			id          TEXT PRIMARY KEY,
			commit_sha  TEXT NOT NULL,
			repo_url    TEXT NOT NULL,
			repo_name   TEXT NOT NULL,
			branch      TEXT NOT NULL,
			author      TEXT NOT NULL,
			message     TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'pending',
			output      TEXT NOT NULL DEFAULT '',
			runner_id   TEXT NOT NULL DEFAULT '',
			started_at  DATETIME,
			finished_at DATETIME,
			created_at  DATETIME NOT NULL,
			updated_at  DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_jobs_status  ON jobs(status);
		CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at DESC);
	`)
	return err
}

// ── Webhook Handler ───────────────────────────────────────────────────────────

func (d *Dispatcher) verifySignature(body []byte, sig string) bool {
	if d.webhookSecret == "" {
		return true
	}
	mac := hmac.New(sha256.New, []byte(d.webhookSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

func (d *Dispatcher) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	// Verify GitHub signature
	sig := r.Header.Get("X-Hub-Signature-256")
	if !d.verifySignature(body, sig) {
		log.Println("webhook: invalid signature — request rejected")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Only handle push events
	if r.Header.Get("X-GitHub-Event") != "push" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ignored")
		return
	}

	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// Only test the allowed repo
	if d.allowedRepo != "" && payload.Repository.FullName != d.allowedRepo {
		log.Printf("webhook: ignoring repo %s (only testing %s)",
			payload.Repository.FullName, d.allowedRepo)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "repo not watched")
		return
	}

	if payload.HeadCommit.ID == "" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "no commit")
		return
	}

	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	now := time.Now()

	job := &Job{
		ID:        uuid.New().String(),
		CommitSHA: payload.HeadCommit.ID,
		RepoURL:   payload.Repository.CloneURL,
		RepoName:  payload.Repository.FullName,
		Branch:    branch,
		Author:    payload.HeadCommit.Author.Name,
		Message:   payload.HeadCommit.Message,
		Status:    StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := d.saveJob(job); err != nil {
		log.Printf("save job: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("✅ new job %s — commit %s by %s on %s",
		job.ID[:8], job.CommitSHA[:8], job.Author, job.Branch)

	d.jobQueue <- job

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"job_id": job.ID})
}

// ── Runner Registration & Heartbeat ──────────────────────────────────────────

func (d *Dispatcher) handleRegisterRunner(w http.ResponseWriter, r *http.Request) {
	var reg struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	d.mu.Lock()
	d.runners[reg.ID] = &Runner{
		ID:       reg.ID,
		Address:  reg.Address,
		LastSeen: time.Now(),
	}
	d.mu.Unlock()

	log.Printf("🤖 runner registered: %s at %s", reg.ID[:8], reg.Address)
	json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

func (d *Dispatcher) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	d.mu.Lock()
	if runner, ok := d.runners[hb.ID]; ok {
		runner.LastSeen = time.Now()
	}
	d.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (d *Dispatcher) handleJobResult(w http.ResponseWriter, r *http.Request) {
	var result struct {
		JobID    string    `json:"job_id"`
		RunnerID string    `json:"runner_id"`
		Status   JobStatus `json:"status"`
		Output   string    `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	now := time.Now()
	_, err := d.db.Exec(`
		UPDATE jobs
		SET status = ?, output = ?, runner_id = ?, finished_at = ?, updated_at = ?
		WHERE id = ?`,
		result.Status, result.Output, result.RunnerID, now, now, result.JobID)
	if err != nil {
		log.Printf("update job result: %v", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	// Free the runner
	d.mu.Lock()
	if runner, ok := d.runners[result.RunnerID]; ok {
		runner.Busy = false
		runner.CurrentJob = ""
	}
	d.mu.Unlock()

	icon := "✅"
	if result.Status == StatusFailed {
		icon = "❌"
	}
	log.Printf("%s job %s finished: %s (runner %s)",
		icon, result.JobID[:8], result.Status, result.RunnerID[:8])

	w.WriteHeader(http.StatusOK)
}

// ── API Endpoints ─────────────────────────────────────────────────────────────

func (d *Dispatcher) handleGetJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := d.db.Query(`
		SELECT id, commit_sha, repo_url, repo_name, branch, author, message,
		       status, output, runner_id, started_at, finished_at, created_at, updated_at
		FROM jobs ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		err := rows.Scan(
			&j.ID, &j.CommitSHA, &j.RepoURL, &j.RepoName,
			&j.Branch, &j.Author, &j.Message, &j.Status,
			&j.Output, &j.RunnerID, &j.StartedAt, &j.FinishedAt,
			&j.CreatedAt, &j.UpdatedAt,
		)
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}

	if jobs == nil {
		jobs = []Job{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

func (d *Dispatcher) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	row := d.db.QueryRow(`
		SELECT id, commit_sha, repo_url, repo_name, branch, author, message,
		       status, output, runner_id, started_at, finished_at, created_at, updated_at
		FROM jobs WHERE id = ?`, id)

	var j Job
	err := row.Scan(
		&j.ID, &j.CommitSHA, &j.RepoURL, &j.RepoName,
		&j.Branch, &j.Author, &j.Message, &j.Status,
		&j.Output, &j.RunnerID, &j.StartedAt, &j.FinishedAt,
		&j.CreatedAt, &j.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(j)
}

func (d *Dispatcher) handleGetRunners(w http.ResponseWriter, r *http.Request) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var runners []*Runner
	for _, r := range d.runners {
		runners = append(runners, r)
	}
	if runners == nil {
		runners = []*Runner{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(runners)
}

// ── DB Helpers ────────────────────────────────────────────────────────────────

func (d *Dispatcher) saveJob(j *Job) error {
	_, err := d.db.Exec(`
		INSERT INTO jobs (id, commit_sha, repo_url, repo_name, branch, author,
		                  message, status, output, runner_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.CommitSHA, j.RepoURL, j.RepoName, j.Branch,
		j.Author, j.Message, j.Status, j.Output, j.RunnerID,
		j.CreatedAt, j.UpdatedAt,
	)
	return err
}

// ── Dispatch Loop ─────────────────────────────────────────────────────────────

func (d *Dispatcher) dispatchLoop() {
	for job := range d.jobQueue {
		go d.dispatchJob(job)
	}
}

func (d *Dispatcher) dispatchJob(job *Job) {
	log.Printf("🔍 looking for runner for job %s", job.ID[:8])

	for {
		runner := d.findFreeRunner()
		if runner == nil {
			log.Printf("⏳ no free runners, waiting 5s for job %s", job.ID[:8])
			time.Sleep(5 * time.Second)
			continue
		}

		if err := d.sendToRunner(runner, job); err != nil {
			log.Printf("❌ runner %s failed: %v — retrying", runner.ID[:8], err)
			d.mu.Lock()
			runner.Busy = false
			runner.CurrentJob = ""
			d.mu.Unlock()
			time.Sleep(2 * time.Second)
			continue
		}
		return
	}
}

func (d *Dispatcher) findFreeRunner() *Runner {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, r := range d.runners {
		if !r.Busy && time.Since(r.LastSeen) < 30*time.Second {
			r.Busy = true
			return r
		}
	}
	return nil
}

func (d *Dispatcher) sendToRunner(runner *Runner, job *Job) error {
	payload, _ := json.Marshal(map[string]string{
		"job_id":     job.ID,
		"commit_sha": job.CommitSHA,
		"repo_url":   job.RepoURL,
		"repo_name":  job.RepoName,
	})

	resp, err := http.Post(
		fmt.Sprintf("http://%s/run", runner.Address),
		"application/json",
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("runner returned %d", resp.StatusCode)
	}

	// Mark job as running with start time
	now := time.Now()
	d.db.Exec(`UPDATE jobs SET status = ?, runner_id = ?, started_at = ?, updated_at = ? WHERE id = ?`,
		StatusRunning, runner.ID, now, now, job.ID)

	d.mu.Lock()
	runner.CurrentJob = job.ID
	d.mu.Unlock()

	log.Printf("🚀 job %s sent to runner %s", job.ID[:8], runner.ID[:8])
	return nil
}

// ── Stale Runner Cleanup ──────────────────────────────────────────────────────

func (d *Dispatcher) cleanupLoop() {
	for {
		time.Sleep(15 * time.Second)
		d.mu.Lock()
		for id, r := range d.runners {
			if time.Since(r.LastSeen) > 30*time.Second {
				log.Printf("🗑️  removing stale runner %s", id[:8])

				// Re-queue its job if it had one
				if r.CurrentJob != "" {
					d.db.Exec(`UPDATE jobs SET status = ?, updated_at = ? WHERE id = ?`,
						StatusPending, time.Now(), r.CurrentJob)

					row := d.db.QueryRow(`
						SELECT id, commit_sha, repo_url, repo_name, branch,
						       author, message, status, output, runner_id,
						       started_at, finished_at, created_at, updated_at
						FROM jobs WHERE id = ?`, r.CurrentJob)

					var j Job
					if err := row.Scan(
						&j.ID, &j.CommitSHA, &j.RepoURL, &j.RepoName,
						&j.Branch, &j.Author, &j.Message, &j.Status,
						&j.Output, &j.RunnerID, &j.StartedAt, &j.FinishedAt,
						&j.CreatedAt, &j.UpdatedAt,
					); err == nil {
						d.jobQueue <- &j
					}
				}
				delete(d.runners, id)
			}
		}
		d.mu.Unlock()
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	dbPath        := getEnv("DB_PATH",        "/data/ci.db")
	port          := getEnv("PORT",           "8080")
	webhookSecret := getEnv("WEBHOOK_SECRET", "")
	allowedRepo   := getEnv("ALLOWED_REPO",   "nad279444/custom-ci")

	log.Printf("🚀 dispatcher starting on :%s", port)
	log.Printf("📦 watching repo: %s", allowedRepo)

	d, err := NewDispatcher(dbPath, webhookSecret, allowedRepo)
	if err != nil {
		log.Fatalf("create dispatcher: %v", err)
	}

	go d.dispatchLoop()
	go d.cleanupLoop()

	mux := http.NewServeMux()

	// GitHub webhook
	mux.HandleFunc("/webhook", d.handleWebhook)

	// Runner communication
	mux.HandleFunc("/runner/register",  d.handleRegisterRunner)
	mux.HandleFunc("/runner/heartbeat", d.handleHeartbeat)
	mux.HandleFunc("/runner/result",    d.handleJobResult)

	// API for UI
	mux.HandleFunc("/api/jobs",   d.handleGetJobs)
	mux.HandleFunc("/api/jobs/",  d.handleGetJob)
	mux.HandleFunc("/api/runners", d.handleGetRunners)

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})

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
