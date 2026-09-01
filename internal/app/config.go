package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddress         string
	DataDir               string
	CaptureMaxBytes       int64
	RetentionDays         int
	MaxRequestsPerProject int
	UpstreamHeaderTimeout time.Duration
}

func LoadConfig() (Config, error) {
	cfg := Config{
		ListenAddress:         envString("LLMPROXY_LISTEN", "127.0.0.1:8080"),
		DataDir:               envString("LLMPROXY_DATA_DIR", "./data"),
		CaptureMaxBytes:       32 << 20,
		RetentionDays:         7,
		MaxRequestsPerProject: 10_000,
		UpstreamHeaderTimeout: 5 * time.Minute,
	}

	var err error
	if cfg.CaptureMaxBytes, err = envInt64("LLMPROXY_CAPTURE_MAX_BYTES", cfg.CaptureMaxBytes); err != nil {
		return Config{}, err
	}
	if cfg.RetentionDays, err = envInt("LLMPROXY_RETENTION_DAYS", cfg.RetentionDays); err != nil {
		return Config{}, err
	}
	if cfg.MaxRequestsPerProject, err = envInt("LLMPROXY_MAX_REQUESTS_PER_PROJECT", cfg.MaxRequestsPerProject); err != nil {
		return Config{}, err
	}
	if cfg.UpstreamHeaderTimeout, err = envDuration("LLMPROXY_UPSTREAM_HEADER_TIMEOUT", cfg.UpstreamHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.CaptureMaxBytes < 1024 {
		return Config{}, fmt.Errorf("LLMPROXY_CAPTURE_MAX_BYTES must be at least 1024")
	}
	if cfg.RetentionDays < 0 || cfg.MaxRequestsPerProject < 0 {
		return Config{}, fmt.Errorf("retention values cannot be negative")
	}
	abs, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data directory: %w", err)
	}
	cfg.DataDir = abs
	return cfg, nil
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return n, nil
}

func envInt64(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return n, nil
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration such as 30s or 5m: %w", name, err)
	}
	return d, nil
}
