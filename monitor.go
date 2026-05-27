package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// ServiceStatus represents the current state of a monitored service.
type ServiceStatus struct {
	Name        string        `json:"name"`
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	Type        string        `json:"type"`
	Node        string        `json:"node"`
	Status      string        `json:"status"` // "up", "down", "degraded", "unknown"
	Latency     time.Duration `json:"latency"`
	LastCheck   time.Time     `json:"last_check"`
	LastUp      time.Time     `json:"last_up"`
	Message     string        `json:"message,omitempty"`
	FailCount   int           `json:"fail_count"`
}

// K8sResource represents a Kubernetes resource health summary.
type K8sResource struct {
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Status    string    `json:"status"`
	Node      string    `json:"node,omitempty"`
	Ready     bool      `json:"ready"`
	LastCheck time.Time `json:"last_check"`
}

// CheckResult stores a single check result for history.
type CheckResult struct {
	Service   string        `json:"service"`
	Status    string        `json:"status"`
	Latency   time.Duration `json:"latency"`
	Timestamp time.Time     `json:"timestamp"`
	Message   string        `json:"message,omitempty"`
}

// Monitor is the health check engine.
type Monitor struct {
	cfg      *Config
	client   *http.Client
	mu       sync.RWMutex
	services map[string]*ServiceStatus
	k8sPods  []K8sResource
	k8sNodes []K8sResource
	history  []CheckResult
}

// NewMonitor creates a new monitor instance.
func NewMonitor(cfg *Config) *Monitor {
	m := &Monitor{
		cfg: cfg,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		services: make(map[string]*ServiceStatus),
		history:  make([]CheckResult, 0, 1000),
	}

	// Initialize service statuses.
	for _, svc := range cfg.Services {
		m.services[svc.Name] = &ServiceStatus{
			Name:   svc.Name,
			Host:   svc.Host,
			Port:   svc.Port,
			Type:   svc.Type,
			Node:   svc.Node,
			Status: "unknown",
		}
	}

	return m
}

// Start begins the monitoring loop.
func (m *Monitor) Start(ctx context.Context) {
	log.Println("[monitor] starting health check engine")

	// Run initial check immediately.
	m.checkAll()

	ticker := time.NewTicker(m.cfg.Server.Poll.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[monitor] stopping health check engine")
			return
		case <-ticker.C:
			m.checkAll()
		}
	}
}

// checkAll runs all health checks.
func (m *Monitor) checkAll() {
	var wg sync.WaitGroup

	// Check services in parallel.
	for _, svc := range m.cfg.Services {
		wg.Add(1)
		go func(svc ServiceConfig) {
			defer wg.Done()
			m.checkService(svc)
		}(svc)
	}

	// Check K8s resources.
	if m.cfg.K8s.Enabled {
		wg.Add(2)
		go func() {
			defer wg.Done()
			m.checkK8sPods()
		}()
		go func() {
			defer wg.Done()
			m.checkK8sNodes()
		}()
	}

	wg.Wait()
}

// checkService performs a health check on a single service.
func (m *Monitor) checkService(svc ServiceConfig) {
	timeout := svc.Timeout.Duration
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	var status string
	var latency time.Duration
	var msg string

	switch svc.Type {
	case "http":
		status, latency, msg = m.checkHTTP(svc, timeout)
	case "tcp":
		status, latency, msg = m.checkTCP(svc, timeout)
	default:
		status, latency, msg = m.checkTCP(svc, timeout)
	}

	now := time.Now()

	m.mu.Lock()
	s := m.services[svc.Name]
	previousStatus := s.Status

	s.Status = status
	s.Latency = latency
	s.LastCheck = now
	s.Message = msg

	if status == "up" {
		s.LastUp = now
		s.FailCount = 0
	} else {
		s.FailCount++
	}
	currentFailCount := s.FailCount
	m.mu.Unlock()

	// Record history.
	m.addHistory(svc.Name, status, latency, msg)

	// Check for status transitions (used by alert system).
	if previousStatus == "up" && status == "down" {
		log.Printf("[monitor] ALERT: %s is DOWN (consecutive failures: %d)", svc.Name, currentFailCount)
	}
	if previousStatus == "down" && status == "up" {
		log.Printf("[monitor] RECOVERY: %s is UP", svc.Name)
	}
}

// checkHTTP performs an HTTP health check.
func (m *Monitor) checkHTTP(svc ServiceConfig, timeout time.Duration) (string, time.Duration, string) {
	url := fmt.Sprintf("http://%s:%d%s", svc.Host, svc.Port, svc.Path)

	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Get(url)
	latency := time.Since(start)

	if err != nil {
		return "down", latency, fmt.Sprintf("connection failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return "up", latency, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return "degraded", latency, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return "down", latency, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// checkTCP performs a TCP port check.
func (m *Monitor) checkTCP(svc ServiceConfig, timeout time.Duration) (string, time.Duration, string) {
	addr := net.JoinHostPort(svc.Host, fmt.Sprintf("%d", svc.Port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	latency := time.Since(start)

	if err != nil {
		return "down", latency, fmt.Sprintf("TCP connection failed: %v", err)
	}
	conn.Close()
	return "up", latency, "TCP port open"
}

// checkK8sPods checks the health of all Kubernetes pods.
func (m *Monitor) checkK8sPods() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "get", "pods", "-A", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[monitor] kubectl get pods failed: %v", err)
		return
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase  string `json:"phase"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(output, &podList); err != nil {
		log.Printf("[monitor] failed to parse pod JSON: %v", err)
		return
	}

	now := time.Now()
	pods := make([]K8sResource, 0, len(podList.Items))
	for _, pod := range podList.Items {
		ready := false
		for _, cond := range pod.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}

		status := pod.Status.Phase
		if status == "Running" && !ready {
			status = "NotReady"
		}

		pods = append(pods, K8sResource{
			Kind:      "Pod",
			Name:      pod.Metadata.Name,
			Namespace: pod.Metadata.Namespace,
			Status:    status,
			Node:      pod.Spec.NodeName,
			Ready:     ready,
			LastCheck: now,
		})
	}

	m.mu.Lock()
	m.k8sPods = pods
	m.mu.Unlock()
}

// checkK8sNodes checks the health of all Kubernetes nodes.
func (m *Monitor) checkK8sNodes() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "get", "nodes", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[monitor] kubectl get nodes failed: %v", err)
		return
	}

	var nodeList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(output, &nodeList); err != nil {
		log.Printf("[monitor] failed to parse node JSON: %v", err)
		return
	}

	now := time.Now()
	nodes := make([]K8sResource, 0, len(nodeList.Items))
	for _, node := range nodeList.Items {
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" {
				ready = true
				break
			}
		}

		status := "NotReady"
		if ready {
			status = "Ready"
		}

		nodes = append(nodes, K8sResource{
			Kind:      "Node",
			Name:      node.Metadata.Name,
			Status:    status,
			Ready:     ready,
			LastCheck: now,
		})
	}

	m.mu.Lock()
	m.k8sNodes = nodes
	m.mu.Unlock()
}

// addHistory appends a check result to the history buffer.
func (m *Monitor) addHistory(service, status string, latency time.Duration, msg string) {
	result := CheckResult{
		Service:   service,
		Status:    status,
		Latency:   latency,
		Timestamp: time.Now(),
		Message:   msg,
	}

	m.mu.Lock()
	m.history = append(m.history, result)

	// Trim old entries beyond max age.
	cutoff := time.Now().Add(-m.cfg.History.MaxAge.Duration)
	i := 0
	for i < len(m.history) && m.history[i].Timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.history = m.history[i:]
	}
	m.mu.Unlock()
}

// GetServices returns a snapshot of all service statuses.
func (m *Monitor) GetServices() []ServiceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ServiceStatus, 0, len(m.services))
	for _, s := range m.services {
		result = append(result, *s)
	}
	return result
}

// GetK8sPods returns a snapshot of all pod statuses.
func (m *Monitor) GetK8sPods() []K8sResource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]K8sResource, len(m.k8sPods))
	copy(result, m.k8sPods)
	return result
}

// GetK8sNodes returns a snapshot of all node statuses.
func (m *Monitor) GetK8sNodes() []K8sResource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]K8sResource, len(m.k8sNodes))
	copy(result, m.k8sNodes)
	return result
}

// GetHistory returns check results since the given time.
func (m *Monitor) GetHistory(since time.Time) []CheckResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]CheckResult, 0)
	for _, h := range m.history {
		if h.Timestamp.After(since) {
			result = append(result, h)
		}
	}
	return result
}

// GetFailCount returns consecutive failure count for a service.
func (m *Monitor) GetFailCount(name string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.services[name]; ok {
		return s.FailCount
	}
	return 0
}

// GetPreviousStatus returns the status before the last check.
func (m *Monitor) IsNewlyDown(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.services[name]
	if !ok {
		return false
	}
	return s.Status == "down" && s.FailCount == m.cfg.Alerts.Threshold
}

// IsNewlyRecovered checks if a service just came back up.
func (m *Monitor) IsNewlyRecovered(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.services[name]
	if !ok {
		return false
	}
	return s.Status == "up" && !s.LastUp.IsZero() && time.Since(s.LastUp) < m.cfg.Server.Poll.Duration*2
}
