package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type JobStatus string

type Job struct {
	ID         string     `json:"id"`
	CommitSHA  string     `json:"commit_sha"`
	RepoURL    string     `json:"repo_url"`
	RepoName   string     `json:"repo_name"`
	Branch     string     `json:"branch"`
	Author     string     `json:"author"`
	Message    string     `json:"message"`
	Status     JobStatus  `json:"status"`
	Output     string     `json:"output"`
	RunnerID   string     `json:"runner_id"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (j Job) Duration() string {
	if j.StartedAt == nil || j.FinishedAt == nil {
		return "—"
	}
	d := j.FinishedAt.Sub(*j.StartedAt)
	if d.Seconds() < 60 {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (j Job) ShortSHA() string {
	if len(j.CommitSHA) >= 8 {
		return j.CommitSHA[:8]
	}
	return j.CommitSHA
}

func (j Job) ShortRunner() string {
	if j.RunnerID == "" {
		return "—"
	}
	if len(j.RunnerID) >= 8 {
		return "runner-" + j.RunnerID[:8]
	}
	return j.RunnerID
}

type Runner struct {
	ID       string    `json:"id"`
	Address  string    `json:"address"`
	Busy     bool      `json:"busy"`
	LastSeen time.Time `json:"last_seen"`
}

func (r Runner) ShortID() string {
	if len(r.ID) >= 8 {
		return "runner-" + r.ID[:8]
	}
	return r.ID
}

var dispatcherURL string

func fetchJobs() ([]Job, error) {
	resp, err := http.Get(dispatcherURL + "/api/jobs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var jobs []Job
	json.NewDecoder(resp.Body).Decode(&jobs)
	return jobs, nil
}

func fetchJob(id string) (*Job, error) {
	resp, err := http.Get(dispatcherURL + "/api/jobs/" + id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	body, _ := io.ReadAll(resp.Body)
	var job Job
	json.Unmarshal(body, &job)
	return &job, nil
}

func fetchRunners() ([]Runner, error) {
	resp, err := http.Get(dispatcherURL + "/api/runners")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var runners []Runner
	json.NewDecoder(resp.Body).Decode(&runners)
	return runners, nil
}

// ── Template Helpers ──────────────────────────────────────────────────────────

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func statusIcon(s JobStatus) string {
	switch s {
	case "passed":  return "✅"
	case "failed":  return "❌"
	case "running": return "🔄"
	case "pending": return "⏳"
	default:        return "⚠️"
	}
}

func statusClass(s JobStatus) string {
	switch s {
	case "passed":  return "passed"
	case "failed":  return "failed"
	case "running": return "running"
	case "pending": return "pending"
	default:        return "error"
	}
}

type Stats struct {
	Total, Passed, Failed, Running, Pending int
	PassRate                                string
}

func calcStats(jobs []Job) Stats {
	s := Stats{Total: len(jobs)}
	for _, j := range jobs {
		switch j.Status {
		case "passed":  s.Passed++
		case "failed":  s.Failed++
		case "running": s.Running++
		case "pending": s.Pending++
		}
	}
	if s.Total > 0 {
		finished := s.Passed + s.Failed
		if finished > 0 {
			s.PassRate = fmt.Sprintf("%.0f%%", float64(s.Passed)/float64(finished)*100)
		} else {
			s.PassRate = "—"
		}
	} else {
		s.PassRate = "—"
	}
	return s
}

var funcMap = template.FuncMap{
	"timeAgo":     timeAgo,
	"statusIcon":  statusIcon,
	"statusClass": statusClass,
}

// ── Dashboard Template ────────────────────────────────────────────────────────

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>custom-ci dashboard</title>
<meta http-equiv="refresh" content="15">
<style>
:root{--bg:#0d1117;--surface:#161b22;--surface2:#1c2128;--border:#30363d;--text:#e6edf3;--muted:#8b949e;--green:#3fb950;--red:#f85149;--yellow:#d29922;--blue:#58a6ff;--purple:#bc8cff}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;min-height:100vh}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}

header{background:var(--surface);border-bottom:1px solid var(--border);padding:14px 24px;display:flex;align-items:center;justify-content:space-between}
.header-left{display:flex;align-items:center;gap:10px}
.logo{font-size:1.1rem;font-weight:700;letter-spacing:-0.02em}
.logo span{color:var(--blue)}
.live-dot{width:8px;height:8px;border-radius:50%;background:var(--green);box-shadow:0 0 8px var(--green);animation:pulse 2s infinite}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.4}}
.refresh-note{font-size:.75rem;color:var(--muted)}

.container{max-width:1200px;margin:0 auto;padding:24px}

.stats{display:grid;grid-template-columns:repeat(5,1fr);gap:12px;margin-bottom:24px}
.stat{background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:16px;text-align:center}
.stat-value{font-size:1.8rem;font-weight:700;line-height:1}
.stat-label{font-size:.72rem;color:var(--muted);margin-top:6px;text-transform:uppercase;letter-spacing:.05em}
.stat-total .stat-value{color:var(--text)}
.stat-passed .stat-value{color:var(--green)}
.stat-failed .stat-value{color:var(--red)}
.stat-running .stat-value{color:var(--blue)}
.stat-passrate .stat-value{color:var(--purple)}

.layout{display:grid;grid-template-columns:1fr 260px;gap:20px}

.card{background:var(--surface);border:1px solid var(--border);border-radius:8px;overflow:hidden}
.card-header{padding:12px 18px;border-bottom:1px solid var(--border);font-size:.8rem;font-weight:600;color:var(--muted);text-transform:uppercase;letter-spacing:.05em;display:flex;justify-content:space-between;align-items:center}

table{width:100%;border-collapse:collapse}
th{padding:10px 16px;text-align:left;font-size:.75rem;color:var(--muted);font-weight:500;border-bottom:1px solid var(--border)}
td{padding:11px 16px;border-bottom:1px solid var(--border);font-size:.85rem;vertical-align:middle}
tr:last-child td{border-bottom:none}
tr:hover td{background:rgba(255,255,255,.02)}

.badge{display:inline-flex;align-items:center;gap:4px;padding:3px 9px;border-radius:10px;font-size:.75rem;font-weight:600;white-space:nowrap}
.badge-passed{background:rgba(63,185,80,.15);color:var(--green)}
.badge-failed{background:rgba(248,81,73,.15);color:var(--red)}
.badge-running{background:rgba(88,166,255,.15);color:var(--blue)}
.badge-pending{background:rgba(210,153,34,.15);color:var(--yellow)}
.badge-error{background:rgba(188,140,255,.15);color:var(--purple)}

.sha{font-family:'SFMono-Regular',Consolas,monospace;background:rgba(255,255,255,.06);padding:2px 6px;border-radius:4px;font-size:.78rem}
.commit-msg{max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--muted);font-size:.8rem}
.duration{font-family:monospace;font-size:.8rem;color:var(--muted)}

.runner-item{padding:12px 18px;border-bottom:1px solid var(--border);display:flex;align-items:center;gap:10px}
.runner-item:last-child{border-bottom:none}
.runner-dot{width:8px;height:8px;border-radius:50%;flex-shrink:0}
.runner-dot.idle{background:var(--green)}
.runner-dot.busy{background:var(--yellow);animation:pulse 1s infinite}
.runner-info{flex:1;min-width:0}
.runner-name{font-size:.82rem;font-family:monospace}
.runner-state{font-size:.75rem;color:var(--muted);margin-top:2px}
.empty{padding:40px;text-align:center;color:var(--muted);font-size:.875rem}
</style>
</head>
<body>
<header>
  <div class="header-left">
    <div class="live-dot"></div>
    <div class="logo">custom<span>-ci</span></div>
  </div>
  <div class="refresh-note">auto-refresh every 15s</div>
</header>

<div class="container">
  <div class="stats">
    <div class="stat stat-total">
      <div class="stat-value">{{.Stats.Total}}</div>
      <div class="stat-label">Total</div>
    </div>
    <div class="stat stat-passed">
      <div class="stat-value">{{.Stats.Passed}}</div>
      <div class="stat-label">Passed</div>
    </div>
    <div class="stat stat-failed">
      <div class="stat-value">{{.Stats.Failed}}</div>
      <div class="stat-label">Failed</div>
    </div>
    <div class="stat stat-running">
      <div class="stat-value">{{.Stats.Running}}</div>
      <div class="stat-label">Running</div>
    </div>
    <div class="stat stat-passrate">
      <div class="stat-value">{{.Stats.PassRate}}</div>
      <div class="stat-label">Pass Rate</div>
    </div>
  </div>

  <div class="layout">
    <div class="card">
      <div class="card-header">
        <span>Recent Jobs</span>
        <span>last 50</span>
      </div>
      {{if .Jobs}}
      <table>
        <thead>
          <tr>
            <th>Status</th>
            <th>Commit</th>
            <th>Message</th>
            <th>Branch</th>
            <th>Author</th>
            <th>Runner</th>
            <th>Duration</th>
            <th>Time</th>
          </tr>
        </thead>
        <tbody>
        {{range .Jobs}}
        <tr>
          <td>
            <a href="/job/{{.ID}}">
              <span class="badge badge-{{statusClass .Status}}">
                {{statusIcon .Status}} {{.Status}}
              </span>
            </a>
          </td>
          <td><span class="sha">{{.ShortSHA}}</span></td>
          <td><div class="commit-msg" title="{{.Message}}">{{.Message}}</div></td>
          <td>{{.Branch}}</td>
          <td>{{.Author}}</td>
          <td><span class="sha">{{.ShortRunner}}</span></td>
          <td><span class="duration">{{.Duration}}</span></td>
          <td title="{{.CreatedAt}}">{{timeAgo .CreatedAt}}</td>
        </tr>
        {{end}}
        </tbody>
      </table>
      {{else}}
      <div class="empty">
        No jobs yet.<br>Push a commit to custom-ci to get started!
      </div>
      {{end}}
    </div>

    <div class="card">
      <div class="card-header">
        <span>Runners</span>
        <span>{{len .Runners}} online</span>
      </div>
      {{if .Runners}}
        {{range .Runners}}
        <div class="runner-item">
          <div class="runner-dot {{if .Busy}}busy{{else}}idle{{end}}"></div>
          <div class="runner-info">
            <div class="runner-name">{{.ShortID}}</div>
            <div class="runner-state">
              {{if .Busy}}🔄 running a job{{else}}✅ idle{{end}}
            </div>
          </div>
        </div>
        {{end}}
      {{else}}
      <div class="empty">No runners connected</div>
      {{end}}
    </div>
  </div>
</div>
</body>
</html>`

// ── Job Detail Template ───────────────────────────────────────────────────────

const jobDetailHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Job.ShortSHA}} — custom-ci</title>
{{if eq .Job.Status "running"}}<meta http-equiv="refresh" content="5">{{end}}
<style>
:root{--bg:#0d1117;--surface:#161b22;--border:#30363d;--text:#e6edf3;--muted:#8b949e;--green:#3fb950;--red:#f85149;--yellow:#d29922;--blue:#58a6ff;--purple:#bc8cff}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif}
a{color:var(--blue);text-decoration:none}
header{background:var(--surface);border-bottom:1px solid var(--border);padding:14px 24px;display:flex;align-items:center;gap:16px}
.back{font-size:.85rem;color:var(--muted)}
.back:hover{color:var(--blue)}
h1{font-size:1rem;font-weight:600}
.container{max-width:1000px;margin:24px auto;padding:0 24px}
.meta{background:var(--surface);border:1px solid var(--border);border-radius:8px;padding:20px;margin-bottom:20px;display:grid;grid-template-columns:1fr 1fr;gap:14px}
.meta-item label{font-size:.72rem;color:var(--muted);display:block;margin-bottom:4px;text-transform:uppercase;letter-spacing:.05em}
.badge{display:inline-flex;align-items:center;gap:4px;padding:4px 12px;border-radius:10px;font-size:.85rem;font-weight:600}
.badge-passed{background:rgba(63,185,80,.15);color:var(--green)}
.badge-failed{background:rgba(248,81,73,.15);color:var(--red)}
.badge-running{background:rgba(88,166,255,.15);color:var(--blue)}
.badge-pending{background:rgba(210,153,34,.15);color:var(--yellow)}
.badge-error{background:rgba(188,140,255,.15);color:var(--purple)}
.sha{font-family:monospace;background:rgba(255,255,255,.06);padding:2px 6px;border-radius:4px;font-size:.85rem}
.output-card{background:var(--surface);border:1px solid var(--border);border-radius:8px;overflow:hidden}
.output-header{padding:12px 18px;border-bottom:1px solid var(--border);display:flex;justify-content:space-between;align-items:center;font-size:.82rem;font-weight:600;color:var(--muted)}
.output-header .live{color:var(--blue)}
pre{padding:20px;font-family:'SFMono-Regular',Consolas,monospace;font-size:.78rem;line-height:1.65;overflow-x:auto;white-space:pre-wrap;color:#c9d1d9;max-height:65vh;overflow-y:auto}
.full-sha{font-family:monospace;font-size:.82rem;word-break:break-all}
</style>
</head>
<body>
<header>
  <a class="back" href="/">← Dashboard</a>
  <h1>Job Detail — <span class="sha">{{.Job.ShortSHA}}</span></h1>
</header>
<div class="container">
  <div class="meta">
    <div class="meta-item">
      <label>Status</label>
      <span class="badge badge-{{statusClass .Job.Status}}">
        {{statusIcon .Job.Status}} {{.Job.Status}}
      </span>
    </div>
    <div class="meta-item">
      <label>Repository</label>
      <span>{{.Job.RepoName}}</span>
    </div>
    <div class="meta-item">
      <label>Commit</label>
      <span class="full-sha">{{.Job.CommitSHA}}</span>
    </div>
    <div class="meta-item">
      <label>Branch</label>
      <span>{{.Job.Branch}}</span>
    </div>
    <div class="meta-item">
      <label>Author</label>
      <span>{{.Job.Author}}</span>
    </div>
    <div class="meta-item">
      <label>Runner</label>
      <span class="sha">{{.Job.ShortRunner}}</span>
    </div>
    <div class="meta-item">
      <label>Duration</label>
      <span>{{.Job.Duration}}</span>
    </div>
    <div class="meta-item">
      <label>Created</label>
      <span>{{.Job.CreatedAt.Format "Jan 2, 2006 15:04:05 UTC"}}</span>
    </div>
    <div class="meta-item" style="grid-column:1/-1">
      <label>Commit Message</label>
      <span>{{.Job.Message}}</span>
    </div>
  </div>

  <div class="output-card">
    <div class="output-header">
      <span>Test Output</span>
      {{if eq .Job.Status "running"}}
      <span class="live">🔄 live — refreshing every 5s</span>
      {{else}}
      <span>{{.Job.Duration}}</span>
      {{end}}
    </div>
    <pre>{{if .Job.Output}}{{.Job.Output}}{{else}}Waiting for output...{{end}}</pre>
  </div>
</div>
</body>
</html>`

// ── Handlers ──────────────────────────────────────────────────────────────────

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	jobs, err := fetchJobs()
	if err != nil {
		log.Printf("fetch jobs: %v", err)
		jobs = []Job{}
	}

	runners, _ := fetchRunners()

	tmpl := template.Must(template.New("dashboard").Funcs(funcMap).Parse(dashboardHTML))
	tmpl.Execute(w, map[string]interface{}{
		"Jobs":    jobs,
		"Runners": runners,
		"Stats":   calcStats(jobs),
	})
}

func handleJobDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/job/")
	job, err := fetchJob(id)
	if err != nil || job == nil {
		http.NotFound(w, r)
		return
	}

	tmpl := template.Must(template.New("job").Funcs(funcMap).Parse(jobDetailHTML))
	tmpl.Execute(w, map[string]interface{}{"Job": job})
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	dispatcherURL = getEnv("DISPATCHER_URL", "http://dispatcher:8080")
	port          := getEnv("PORT", "3000")

	log.Printf("🖥️  UI starting on :%s", port)

	mux := http.NewServeMux()
	mux.HandleFunc("/",      handleDashboard)
	mux.HandleFunc("/job/",  handleJobDetail)
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
