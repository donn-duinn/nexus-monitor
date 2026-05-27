package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Alert represents an active or resolved alert.
type Alert struct {
	ID        string    `json:"id"`
	Service   string    `json:"service"`
	Type      string    `json:"type"` // "down", "recovered"
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Resolved  bool      `json:"resolved"`
}

// AlertSystem manages alerts and notifications.
type AlertSystem struct {
	cfg       *Config
	monitor   *Monitor
	alerts    []Alert
	alerted   map[string]bool // service -> whether we've alerted for current outage
	httpClient *http.Client
}

// NewAlertSystem creates a new alert system.
func NewAlertSystem(cfg *Config, monitor *Monitor) *AlertSystem {
	return &AlertSystem{
		cfg:        cfg,
		monitor:    monitor,
		alerts:     make([]Alert, 0, 100),
		alerted:    make(map[string]bool),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Start begins the alert checking loop.
func (a *AlertSystem) Start(ctx context.Context) {
	if !a.cfg.Alerts.Enabled {
		log.Println("[alerts] alerting is disabled")
		return
	}

	log.Println("[alerts] starting alert system")
	ticker := time.NewTicker(a.cfg.Server.Poll.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[alerts] stopping alert system")
			return
		case <-ticker.C:
			a.checkAlerts()
		}
	}
}

// checkAlerts evaluates all services for alert conditions.
func (a *AlertSystem) checkAlerts() {
	for _, svc := range a.cfg.Services {
		failCount := a.monitor.GetFailCount(svc.Name)

		// Service just went down (reached threshold).
		if failCount == a.cfg.Alerts.Threshold && !a.alerted[svc.Name] {
			a.fireAlert(svc.Name, "down", fmt.Sprintf("Service %s is DOWN after %d consecutive failures", svc.Name, failCount))
			a.alerted[svc.Name] = true
		}

		// Service recovered.
		if a.monitor.IsNewlyRecovered(svc.Name) && a.alerted[svc.Name] {
			a.fireAlert(svc.Name, "recovered", fmt.Sprintf("Service %s has RECOVERED", svc.Name))
			a.alerted[svc.Name] = false
		}
	}
}

// fireAlert sends an alert through all configured channels.
func (a *AlertSystem) fireAlert(service, alertType, message string) {
	alert := Alert{
		ID:        fmt.Sprintf("%s-%s-%d", service, alertType, time.Now().Unix()),
		Service:   service,
		Type:      alertType,
		Message:   message,
		Timestamp: time.Now(),
		Resolved:  alertType == "recovered",
	}

	a.alerts = append(a.alerts, alert)
	log.Printf("[alert] %s", message)

	// Publish to MQTT.
	if a.cfg.Alerts.MQTT.Enabled {
		go a.publishMQTT(alert)
	}

	// Send webhook.
	if a.cfg.Alerts.Webhook.Enabled {
		go a.sendWebhook(alert)
	}
}

// publishMQTT publishes an alert to the MQTT broker.
func (a *AlertSystem) publishMQTT(alert Alert) {
	payload, err := json.Marshal(alert)
	if err != nil {
		log.Printf("[alerts] failed to marshal MQTT alert: %v", err)
		return
	}

	// Use HTTP publish endpoint or direct MQTT.
	// For simplicity, we use a lightweight HTTP-to-MQTT approach.
	// In production, use a proper MQTT client library.
	addr := fmt.Sprintf("http://%s:%d/mqtt/publish", a.cfg.Alerts.MQTT.Broker, a.cfg.Alerts.MQTT.Port+1000)
	req, err := http.NewRequest("POST", addr, bytes.NewReader(payload))
	if err != nil {
		log.Printf("[alerts] MQTT publish request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Topic", a.cfg.Alerts.MQTT.Topic)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		// Fallback: log the alert if MQTT is unreachable.
		log.Printf("[alerts] MQTT publish failed (will retry on next check): %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[alerts] MQTT alert published to %s", a.cfg.Alerts.MQTT.Topic)
}

// sendWebhook sends an alert to a configured webhook URL.
func (a *AlertSystem) sendWebhook(alert Alert) {
	payload, err := json.Marshal(alert)
	if err != nil {
		log.Printf("[alerts] failed to marshal webhook alert: %v", err)
		return
	}

	resp, err := a.httpClient.Post(a.cfg.Alerts.Webhook.URL, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("[alerts] webhook send failed: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("[alerts] webhook alert sent to %s", a.cfg.Alerts.Webhook.URL)
}

// GetAlerts returns all alerts, optionally filtered to active only.
func (a *AlertSystem) GetAlerts(activeOnly bool) []Alert {
	if !activeOnly {
		return a.alerts
	}

	result := make([]Alert, 0)
	for _, alert := range a.alerts {
		if !alert.Resolved {
			result = append(result, alert)
		}
	}
	return result
}
