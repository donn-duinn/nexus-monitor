package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
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
	cfg        *Config
	monitor    *Monitor
	mu         sync.RWMutex
	alerts     []Alert
	alerted    map[string]bool // service -> whether we've alerted for current outage
	httpClient *http.Client
	mqttClient mqtt.Client
}

// NewAlertSystem creates a new alert system.
func NewAlertSystem(cfg *Config, monitor *Monitor) *AlertSystem {
	a := &AlertSystem{
		cfg:        cfg,
		monitor:    monitor,
		alerts:     make([]Alert, 0, 100),
		alerted:    make(map[string]bool),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}

	// Initialize MQTT client if enabled.
	if cfg.Alerts.MQTT.Enabled {
		broker := fmt.Sprintf("tcp://%s:%d", cfg.Alerts.MQTT.Broker, cfg.Alerts.MQTT.Port)
		opts := mqtt.NewClientOptions().
			AddBroker(broker).
			SetClientID(cfg.Alerts.MQTT.ClientID).
			SetAutoReconnect(true).
			SetConnectTimeout(10 * time.Second)
		a.mqttClient = mqtt.NewClient(opts)
		if token := a.mqttClient.Connect(); token.Wait() && token.Error() != nil {
			log.Printf("[alerts] MQTT client connect failed: %v", token.Error())
		}
	}

	return a
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

		a.mu.RLock()
		alreadyAlerted := a.alerted[svc.Name]
		a.mu.RUnlock()

		// Service just went down (reached threshold).
		if failCount >= a.cfg.Alerts.Threshold && !alreadyAlerted {
			a.fireAlert(svc.Name, "down", fmt.Sprintf("Service %s is DOWN after %d consecutive failures", svc.Name, failCount))
			a.mu.Lock()
			a.alerted[svc.Name] = true
			a.mu.Unlock()
		}

		// Service recovered.
		if a.monitor.IsNewlyRecovered(svc.Name) && alreadyAlerted {
			a.fireAlert(svc.Name, "recovered", fmt.Sprintf("Service %s has RECOVERED", svc.Name))
			a.mu.Lock()
			a.alerted[svc.Name] = false
			a.mu.Unlock()
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

	a.mu.Lock()
	a.alerts = append(a.alerts, alert)
	a.mu.Unlock()
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
	if a.mqttClient == nil || !a.mqttClient.IsConnected() {
		log.Printf("[alerts] MQTT client not connected, skipping publish")
		return
	}

	payload, err := json.Marshal(alert)
	if err != nil {
		log.Printf("[alerts] failed to marshal MQTT alert: %v", err)
		return
	}

	token := a.mqttClient.Publish(a.cfg.Alerts.MQTT.Topic, 1, false, payload)
	token.Wait()
	if token.Error() != nil {
		log.Printf("[alerts] MQTT publish failed: %v", token.Error())
		return
	}
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

// Disconnect cleanly shuts down the MQTT client.
func (a *AlertSystem) Disconnect() {
	if a.mqttClient != nil && a.mqttClient.IsConnected() {
		a.mqttClient.Disconnect(1000)
	}
}

// GetAlerts returns all alerts, optionally filtered to active only.
func (a *AlertSystem) GetAlerts(activeOnly bool) []Alert {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !activeOnly {
		result := make([]Alert, len(a.alerts))
		copy(result, a.alerts)
		return result
	}

	result := make([]Alert, 0)
	for _, alert := range a.alerts {
		if !alert.Resolved {
			result = append(result, alert)
		}
	}
	return result
}
