# custom-ci

A custom-built Continuous Integration system written in Go, running on Oracle Cloud Free Tier. Every push to this repository automatically runs tests and displays results on a live dashboard.

---

## 🏗️ Architecture

```
git push
    │
    ├── Webhook 1 (port 9000) → deploy.sh → git pull → docker compose up
    │
    └── Webhook 2 (port 80)  → Dispatcher → Runner → go test → Dashboard
```

### Three Go Microservices

```
┌─────────────────────────────────────────────────────┐
│                   Oracle Cloud VM                    │
│                                                      │
│  ┌──────────────┐        ┌────────────────────────┐ │
│  │  Dispatcher  │───────▶│       Runner           │ │
│  │  port :8080  │        │       port :8090        │ │
│  │              │        │                        │ │
│  │ • Receives   │        │ • Clones repo at SHA   │ │
│  │   webhooks   │◀───────│ • Runs go vet          │ │
│  │ • Queues     │        │ • Runs go test -v      │ │
│  │   jobs       │        │ • Reports results      │ │
│  │ • Manages    │        └────────────────────────┘ │
│  │   runners    │                                    │
│  │ • SQLite DB  │        ┌────────────────────────┐ │
│  └──────┬───────┘        │          UI            │ │
│         │                │       port :3000        │ │
│         └───────────────▶│                        │ │
│                          │ • Live dashboard        │ │
│                          │ • Pass/fail per commit  │ │
│                          │ • Full test output      │ │
│                          │ • Runner info           │ │
│                          │ • Build duration        │ │
│                          └────────────────────────┘ │
│                                                      │
│         Nginx reverse proxy (port :80/:443)          │
└─────────────────────────────────────────────────────┘
```

---

## ✅ What Was Built

### Infrastructure

- **Oracle Cloud VM** — Always Free Tier, Ubuntu 22.04, 47GB storage
- **Domain** — Free subdomain via FreeDNS pointing to VM public IP
- **SSL** — Free HTTPS via Let's Encrypt + Certbot
- **Nginx** — Reverse proxy routing traffic to CI services
- **Docker** — All services containerized, starts on reboot
- **Two firewalls configured** — Oracle Security List + Ubuntu iptables

### CI System Components

#### Dispatcher (`dispatcher/main.go`)

- Receives GitHub webhooks with HMAC-SHA256 signature verification
- Only processes pushes to the configured repo (`ALLOWED_REPO`)
- Queues jobs in SQLite database
- Finds free runners and dispatches jobs to them
- Tracks runner heartbeats — removes stale runners after 30s
- Automatically re-queues jobs from crashed runners
- Exposes REST API for the UI

#### Runner (`runner/main.go`)

- Registers with dispatcher on startup
- Sends heartbeat every 10s to stay alive
- Clones repo at exact commit SHA (not branch tip)
- Runs `go mod download` → `go vet ./...` → `go test -v ./...`
- Each job gets an isolated workspace, cleaned up after
- Reports full output + pass/fail status back to dispatcher
- Retries result reporting up to 5 times on failure

#### UI (`ui/main.go`)

- Live dashboard auto-refreshing every 15s
- Shows last 50 jobs with status, commit, author, branch, runner, duration
- Clickable job detail page with full test output
- Live refresh every 5s on running jobs
- Runner pool status (idle/busy)
- Stats: total, passed, failed, running, pass rate

### Auto Deploy

- GitHub webhook triggers `deploy.sh` on every push
- Script runs `git pull` + `docker compose up -d --build`
- Deployment logs saved to `/var/log/deploy.log`
- Webhook listener runs as systemd service (survives reboots)

### Memory Optimisation — Swap File

The VM only has 1GB RAM which is not enough to compile three Go services in parallel. Docker builds were freezing mid-compilation. The fix was to add a 1GB swap file so the OS could use disk space as overflow memory:

```bash
sudo fallocate -l 1G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile

# Make it permanent across reboots
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

After enabling swap the builds completed successfully. Services were then built one at a time to avoid overwhelming the RAM:

```bash
docker compose build dispatcher
docker compose build runner
docker compose build ui
docker compose up -d
```

---

## 📁 Project Structure

```
custom-ci/
│
├── dispatcher/
│   ├── main.go              # Webhook receiver, job queue, runner manager
│   ├── dispatcher_test.go   # Tests for dispatcher logic
│   └── Dockerfile           # Two-stage build with CGO for SQLite
│
├── runner/
│   ├── main.go              # Clones repo, runs tests, reports results
│   ├── runner_test.go       # Tests for runner logic
│   └── Dockerfile           # Keeps Go installed for running go test
│
├── ui/
│   ├── main.go              # Web dashboard
│   ├── ui_test.go           # Tests for UI helper functions
│   └── Dockerfile           # Two-stage build, tiny runtime
│
├── nginx/
│   └── nginx.conf           # Routes /webhook → dispatcher, / → UI
│
├── docker-compose.yml       # Orchestrates all services
├── deploy.sh                # Auto deploy script triggered by webhook
├── go.mod                   # Go module definition
├── go.sum                   # Dependency checksums
├── .env.example             # Environment variable template
├── .gitignore               # Ignores .env and *.db
└── README.md                # This file
```

---

## 🚀 Daily Workflow

```bash
# Write code locally
# Stage and commit
git add .
git commit -m "your message"

# Push — two things happen automatically:
# 1. VM pulls and redeploys your code
# 2. CI runs go test and shows results on dashboard
git push
```

---

## 🔧 How It Works Step by Step

```
1.  You push to GitHub
2.  GitHub sends POST to /webhook (port 80)
3.  Nginx forwards to Dispatcher
4.  Dispatcher verifies HMAC-SHA256 signature
5.  Dispatcher checks repo matches ALLOWED_REPO
6.  Dispatcher creates job in SQLite, puts in queue
7.  Dispatcher finds free Runner, sends job
8.  Runner clones repo at exact commit SHA
9.  Runner runs: go mod download
10. Runner runs: go vet ./...
11. Runner runs: go test -v ./...
12. Runner reports output + status to Dispatcher
13. Dispatcher saves results to SQLite
14. Dashboard shows results at your domain
```

---

## 🛠️ Useful Commands

```bash
# View all service logs
docker compose logs -f

# View specific service logs
docker compose logs -f runner
docker compose logs -f dispatcher

# Check service status
docker compose ps

# Restart all services
docker compose restart

# Watch deploy log
tail -f /var/log/deploy.log

# Check jobs in database
docker compose exec dispatcher sqlite3 /data/ci.db \
  "SELECT id, commit_sha, status, author FROM jobs ORDER BY created_at DESC LIMIT 10;"

# Scale to multiple runners
docker compose up -d --scale runner=2

# Rebuild and restart
docker compose up -d --build
```

---

## ⚙️ Environment Variables

| Variable         | Description                                   |
| ---------------- | --------------------------------------------- |
| `WEBHOOK_SECRET` | HMAC secret shared with GitHub webhook        |
| `ALLOWED_REPO`   | Only test this repo (format: `username/repo`) |
| `DB_PATH`        | SQLite database path (default: `/data/ci.db`) |
| `DISPATCHER_URL` | Runner uses this to find dispatcher           |

---

## 📊 Test Coverage

Tests written for all three services:

| Package      | Tests                                                                      |
| ------------ | -------------------------------------------------------------------------- |
| `dispatcher` | Webhook verification, signature checking, job status, duration, runner API |
| `runner`     | Runner creation, unique IDs, environment variables, status values          |
| `ui`         | Duration formatting, SHA display, stats calculation, time formatting       |

---

_Built on Oracle Cloud Always Free Tier — free forever_
