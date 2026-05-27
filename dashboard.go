package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Dashboard serves the web UI and API endpoints.
type Dashboard struct {
	monitor *Monitor
	alerts  *AlertSystem
}

// NewDashboard creates a new dashboard instance.
func NewDashboard(monitor *Monitor, alerts *AlertSystem) *Dashboard {
	return &Dashboard{
		monitor: monitor,
		alerts:  alerts,
	}
}

// RegisterRoutes registers all dashboard HTTP routes.
func (d *Dashboard) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", d.handleDashboard)
	mux.HandleFunc("/api/status", d.handleAPIStatus)
	mux.HandleFunc("/api/history", d.handleAPIHistory)
	mux.HandleFunc("/api/alerts", d.handleAPIAlerts)
}

// handleDashboard serves the HTML status page.
func (d *Dashboard) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(dashboardHTML))
}

// handleAPIStatus returns JSON status of all services.
func (d *Dashboard) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	type StatusResponse struct {
		Services []ServiceStatus `json:"services"`
		Pods     []K8sResource   `json:"pods,omitempty"`
		Nodes    []K8sResource   `json:"nodes,omitempty"`
		Uptime   string          `json:"uptime"`
	}

	resp := StatusResponse{
		Services: d.monitor.GetServices(),
		Pods:     d.monitor.GetK8sPods(),
		Nodes:    d.monitor.GetK8sNodes(),
		Uptime:   time.Since(startTime).Round(time.Second).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAPIHistory returns check history for the last 24h.
func (d *Dashboard) handleAPIHistory(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-24 * time.Hour)
	history := d.monitor.GetHistory(since)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleAPIAlerts returns active alerts.
func (d *Dashboard) handleAPIAlerts(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"
	alerts := d.alerts.GetAlerts(activeOnly)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
}

var startTime = time.Now()

func init() {
	// Suppress unused import warning.
	_ = log.New(nil, "", 0)
}

// dashboardHTML is the self-contained status page.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Nexus Monitor - Tech Duinn Swarm</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, monospace;
  background: #0d1117;
  color: #c9d1d9;
  min-height: 100vh;
  padding: 2rem;
}
h1 {
  font-size: 1.5rem;
  color: #58a6ff;
  margin-bottom: 0.5rem;
}
.subtitle {
  color: #8b949e;
  font-size: 0.85rem;
  margin-bottom: 2rem;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1rem;
  margin-bottom: 2rem;
}
.card {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 1rem 1.25rem;
  transition: border-color 0.2s;
}
.card:hover { border-color: #58a6ff; }
.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.75rem;
}
.card-name {
  font-size: 1rem;
  font-weight: 600;
  color: #e6edf3;
}
.badge {
  display: inline-block;
  padding: 0.2em 0.6em;
  border-radius: 12px;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
}
.badge-up { background: #238636; color: #fff; }
.badge-down { background: #da3633; color: #fff; }
.badge-degraded { background: #d29922; color: #fff; }
.badge-unknown { background: #484f58; color: #fff; }
.card-detail {
  font-size: 0.8rem;
  color: #8b949e;
  margin-top: 0.25rem;
}
.card-detail span { color: #c9d1d9; }
.section-title {
  font-size: 1.1rem;
  color: #e6edf3;
  margin: 1.5rem 0 0.75rem;
  padding-bottom: 0.5rem;
  border-bottom: 1px solid #30363d;
}
.alerts-section {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 8px;
  padding: 1rem 1.25rem;
  margin-bottom: 2rem;
}
.alert-item {
  padding: 0.5rem 0;
  border-bottom: 1px solid #21262d;
  font-size: 0.85rem;
}
.alert-item:last-child { border-bottom: none; }
.alert-down { color: #f85149; }
.alert-recovered { color: #3fb950; }
.k8s-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.85rem;
}
.k8s-table th {
  text-align: left;
  padding: 0.5rem;
  border-bottom: 1px solid #30363d;
  color: #8b949e;
  font-weight: 500;
}
.k8s-table td {
  padding: 0.5rem;
  border-bottom: 1px solid #21262d;
}
.footer {
  text-align: center;
  color: #484f58;
  font-size: 0.75rem;
  margin-top: 2rem;
}
</style>
</head>
<body>
<h1>Nexus Monitor</h1>
<div class="subtitle">Tech Duinn Swarm Health &mdash; Auto-refreshing every 10s</div>

<div id="alerts-container"></div>

<div class="section-title">Services</div>
<div id="services-grid" class="grid"></div>

<div class="section-title">Kubernetes Nodes</div>
<div id="nodes-grid" class="grid"></div>

<div class="section-title">Kubernetes Pods</div>
<div id="pods-container"></div>

<div class="footer" id="footer">Loading...</div>

<script>
const statusClass = s => {
  if (s === 'up' || s === 'Running' || s === 'Ready') return 'badge-up';
  if (s === 'down' || s === 'NotReady') return 'badge-down';
  if (s === 'degraded' || s === 'NotReady') return 'badge-degraded';
  return 'badge-unknown';
};

function formatLatency(ns) {
  if (ns < 1000000) return (ns / 1000).toFixed(0) + 'us';
  if (ns < 1000000000) return (ns / 1000000).toFixed(0) + 'ms';
  return (ns / 1000000000).toFixed(1) + 's';
}

function renderServices(services) {
  const grid = document.getElementById('services-grid');
  grid.innerHTML = services.map(s => '<div class="card">' +
    '<div class="card-header">' +
      '<span class="card-name">' + s.name + '</span>' +
      '<span class="badge ' + statusClass(s.status) + '">' + s.status + '</span>' +
    '</div>' +
    '<div class="card-detail">Node: <span>' + s.node + '</span></div>' +
    '<div class="card-detail">Endpoint: <span>' + s.host + ':' + s.port + '</span></div>' +
    '<div class="card-detail">Latency: <span>' + formatLatency(s.latency) + '</span></div>' +
    '<div class="card-detail">Last check: <span>' + new Date(s.last_check).toLocaleTimeString() + '</span></div>' +
    (s.message ? '<div class="card-detail">Message: <span>' + s.message + '</span></div>' : '') +
  '</div>').join('');
}

function renderNodes(nodes) {
  const grid = document.getElementById('nodes-grid');
  if (!nodes || nodes.length === 0) {
    grid.innerHTML = '<div class="card"><div class="card-detail">No node data available</div></div>';
    return;
  }
  grid.innerHTML = nodes.map(n => '<div class="card">' +
    '<div class="card-header">' +
      '<span class="card-name">' + n.name + '</span>' +
      '<span class="badge ' + statusClass(n.status) + '">' + n.status + '</span>' +
    '</div>' +
    '<div class="card-detail">Kind: <span>' + n.kind + '</span></div>' +
    '<div class="card-detail">Last check: <span>' + new Date(n.last_check).toLocaleTimeString() + '</span></div>' +
  '</div>').join('');
}

function renderPods(pods) {
  const container = document.getElementById('pods-container');
  if (!pods || pods.length === 0) {
    container.innerHTML = '<div class="card"><div class="card-detail">No pod data available</div></div>';
    return;
  }
  let html = '<table class="k8s-table"><thead><tr>' +
    '<th>Name</th><th>Namespace</th><th>Node</th><th>Status</th><th>Ready</th>' +
    '</tr></thead><tbody>';
  pods.forEach(p => {
    html += '<tr>' +
      '<td>' + p.name + '</td>' +
      '<td>' + p.namespace + '</td>' +
      '<td>' + (p.node || '-') + '</td>' +
      '<td><span class="badge ' + statusClass(p.status) + '">' + p.status + '</span></td>' +
      '<td>' + (p.ready ? 'Yes' : 'No') + '</td>' +
    '</tr>';
  });
  html += '</tbody></table>';
  container.innerHTML = html;
}

function renderAlerts(alerts) {
  const container = document.getElementById('alerts-container');
  if (!alerts || alerts.length === 0) {
    container.innerHTML = '';
    return;
  }
  let html = '<div class="section-title">Active Alerts</div><div class="alerts-section">';
  alerts.filter(a => !a.resolved).forEach(a => {
    html += '<div class="alert-item alert-' + a.type + '">' +
      '<strong>' + a.type.toUpperCase() + '</strong> ' + a.message +
      ' <span style="color:#484f58">(' + new Date(a.timestamp).toLocaleString() + ')</span>' +
    '</div>';
  });
  html += '</div>';
  container.innerHTML = html;
}

async function refresh() {
  try {
    const [statusResp, alertsResp] = await Promise.all([
      fetch('/api/status'),
      fetch('/api/alerts?active=true')
    ]);
    const status = await statusResp.json();
    const alerts = await alertsResp.json();

    renderServices(status.services || []);
    renderNodes(status.nodes || []);
    renderPods(status.pods || []);
    renderAlerts(alerts || []);

    document.getElementById('footer').textContent =
      'Uptime: ' + status.uptime + ' | Last refresh: ' + new Date().toLocaleTimeString();
  } catch (e) {
    document.getElementById('footer').textContent = 'Error fetching data: ' + e.message;
  }
}

refresh();
setInterval(refresh, 10000);
</script>
</body>
</html>`
