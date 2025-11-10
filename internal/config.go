package worldclass

import (
	"errors"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

const (
	defaultBaseURL = "https://members.worldclass.ro"
	defaultTZ      = "Europe/Bucharest"
)

// Config captures runtime settings loaded from config.yaml.
type Config struct {
	BaseURL     string                         `yaml:"base_url"`
	Timezone    string                         `yaml:"timezone"`
	Credentials Credentials                    `yaml:"credentials"`
	Clubs       []Club                         `yaml:"clubs"`
	Interests   map[string][]ClassInterest     `yaml:"interests"`
	Sentry      SentryConfig                   `yaml:"sentry"`
}

// ClassInterest describes a class the user is interested in tracking or booking.
type ClassInterest struct {
	Day        string `yaml:"day"`
	Time       string `yaml:"time"`
	Title      string `yaml:"title"`
	DayEnglish string `yaml:"day_english"`
}

// SentryConfig groups the optional monitoring settings.
type SentryConfig struct {
	DSN string `yaml:"dsn"`
}

// LoadConfig reads the YAML configuration file from disk.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Timezone == "" {
		cfg.Timezone = defaultTZ
	}
	if cfg.Interests == nil {
		cfg.Interests = make(map[string][]ClassInterest)
	}

	if cfg.Credentials.Email == "" || cfg.Credentials.Password == "" {
		return nil, errors.New("credentials.email and credentials.password must be set")
	}
	if len(cfg.Clubs) == 0 {
		return nil, errors.New("at least one club must be configured")
	}

	return &cfg, nil
}
