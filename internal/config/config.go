package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`
	Webhook WebhookConfig `yaml:"webhook"`
	Logging LoggingConfig `yaml:"logging"`
}

type ServerConfig struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	PublicBaseURL string `yaml:"public_base_url"`
}

type StorageConfig struct {
	DataDir       string `yaml:"data_dir"`
	DBPath        string `yaml:"db_path"`
	STRMOutputDir string `yaml:"strm_output_dir"`
}

type AuthConfig struct {
	BootstrapUsername string `yaml:"bootstrap_username"`
	BootstrapPassword string `yaml:"bootstrap_password"`
}

type WebhookConfig struct {
	Token         string   `yaml:"token"`
	StripPrefixes []string `yaml:"strip_prefixes"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode yaml: %w", err)
	}

	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	baseDir := filepath.Dir(path)
	cfg.Storage.DataDir = resolvePath(baseDir, cfg.Storage.DataDir)
	cfg.Storage.DBPath = resolvePath(baseDir, cfg.Storage.DBPath)
	cfg.Storage.STRMOutputDir = resolvePath(baseDir, cfg.Storage.STRMOutputDir)

	if err := os.MkdirAll(cfg.Storage.DataDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Storage.DBPath), 0o755); err != nil {
		return Config{}, fmt.Errorf("create db dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.STRMOutputDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create strm dir: %w", err)
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.Server.Host == "" {
		return fmt.Errorf("server.host is required")
	}
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be positive")
	}
	if c.Storage.DBPath == "" {
		return fmt.Errorf("storage.db_path is required")
	}
	if c.Storage.STRMOutputDir == "" {
		return fmt.Errorf("storage.strm_output_dir is required")
	}
	return nil
}

func (s ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 7001
	}
	if cfg.Storage.DataDir == "" {
		cfg.Storage.DataDir = "./data"
	}
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = "./data/app.db"
	}
	if cfg.Storage.STRMOutputDir == "" {
		cfg.Storage.STRMOutputDir = "./data/strm"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
}

func resolvePath(baseDir, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}
