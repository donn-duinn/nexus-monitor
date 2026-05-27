package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration with YAML string unmarshaling support.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	s := value.Value
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

// Config holds all monitor configuration.
type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Services []ServiceConfig `yaml:"services"`
	K8s      K8sConfig       `yaml:"k8s"`
	Alerts   AlertsConfig    `yaml:"alerts"`
	History  HistoryConfig   `yaml:"history"`
}

type ServerConfig struct {
	Host string   `yaml:"host"`
	Port int      `yaml:"port"`
	Poll Duration `yaml:"poll_interval"`
}

type ServiceConfig struct {
	Name    string   `yaml:"name"`
	Host    string   `yaml:"host"`
	Port    int      `yaml:"port"`
	Type    string   `yaml:"type"` // "http", "tcp"
	Path    string   `yaml:"path"` // HTTP path for health check
	Timeout Duration `yaml:"timeout"`
	Node    string   `yaml:"node"`
}

type K8sConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
}

type AlertsConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Threshold  int           `yaml:"threshold"` // failures before alert
	MQTT       MQTTConfig    `yaml:"mqtt"`
	Webhook    WebhookConfig `yaml:"webhook"`
}

type MQTTConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Broker   string `yaml:"broker"`
	Port     int    `yaml:"port"`
	Topic    string `yaml:"topic"`
	ClientID string `yaml:"client_id"`
}

type WebhookConfig struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
}

type HistoryConfig struct {
	MaxAge Duration `yaml:"max_age"`
}

// DefaultConfig returns a sensible default configuration for the Tech Duinn swarm.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8099,
			Poll: Duration{30 * time.Second},
		},
		Services: []ServiceConfig{
			{Name: "ollama", Host: "dagda", Port: 11434, Type: "http", Path: "/", Node: "dagda"},
			{Name: "litellm", Host: "dagda", Port: 4000, Type: "http", Path: "/health", Node: "dagda"},
			{Name: "jellyfin", Host: "brigid", Port: 8097, Type: "http", Path: "/health", Node: "brigid"},
			{Name: "pihole", Host: "cernunnos", Port: 80, Type: "http", Path: "/admin/", Node: "cernunnos"},
			{Name: "redis", Host: "dagda", Port: 6379, Type: "tcp", Node: "dagda"},
			{Name: "chroma", Host: "dagda", Port: 8002, Type: "http", Path: "/api/v1/heartbeat", Node: "dagda"},
			{Name: "engram", Host: "dagda", Port: 7437, Type: "http", Path: "/health", Node: "dagda"},
			{Name: "mqtt", Host: "dagda", Port: 1883, Type: "tcp", Node: "dagda"},
		},
		K8s: K8sConfig{
			Enabled:  true,
			Interval: Duration{30 * time.Second},
		},
		Alerts: AlertsConfig{
			Enabled:   true,
			Threshold: 2,
			MQTT: MQTTConfig{
				Enabled:  true,
				Broker:   "dagda",
				Port:     1883,
				Topic:    "nexus/alerts",
				ClientID: "nexus-monitor",
			},
			Webhook: WebhookConfig{
				Enabled: false,
			},
		},
		History: HistoryConfig{
			MaxAge: Duration{24 * time.Hour},
		},
	}
}

// LoadConfig reads configuration from a YAML file, falling back to defaults.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	return cfg, nil
}
