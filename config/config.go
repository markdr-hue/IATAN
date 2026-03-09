/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	AdminPort      int      `json:"admin_port"`
	PublicPort     int      `json:"public_port"`
	DataDir        string   `json:"data_dir"`
	DBPath         string   `json:"db_path"`
	JWTSecret      string   `json:"jwt_secret"`
	EncryptionKey  string   `json:"encryption_key"`
	LogLevel       string   `json:"log_level"`
	FirstRunPath   string   `json:"firstrun_path"`
	CaddyEnabled   bool     `json:"caddy_enabled"`
	CORSOrigins    []string `json:"cors_origins"`
	RateLimitRate  float64  `json:"rate_limit_rate"`
	RateLimitBurst int      `json:"rate_limit_burst"`

	// Brain monitoring intervals (seconds). Zero means use built-in defaults.
	BrainMonitoringBaseSec int `json:"brain_monitoring_base_sec"`
	BrainMonitoringMaxSec  int `json:"brain_monitoring_max_sec"`
}

func Load() (*Config, error) {
	cfg := defaults()

	// Try loading from config.json
	if data, err := os.ReadFile("config.json"); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config.json: %w", err)
		}
	}

	// Environment variable overrides
	applyEnvOverrides(cfg)

	// Ensure data directory exists
	if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}

	// Set DB path relative to data dir if not explicitly set
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "iatan.db")
	}

	// Auto-generate security keys on first run
	securityDir := filepath.Join(cfg.DataDir, ".security")
	if err := os.MkdirAll(securityDir, 0700); err != nil {
		return nil, fmt.Errorf("creating security dir: %w", err)
	}

	if cfg.JWTSecret == "" {
		secret, err := loadOrGenerateKey(filepath.Join(securityDir, "jwt.key"), 32)
		if err != nil {
			return nil, fmt.Errorf("jwt key: %w", err)
		}
		cfg.JWTSecret = secret
	}
	if len(cfg.JWTSecret) < 32 {
		return nil, fmt.Errorf("JWT secret must be at least 32 characters (got %d)", len(cfg.JWTSecret))
	}

	if cfg.EncryptionKey == "" {
		key, err := loadOrGenerateKey(filepath.Join(securityDir, "encryption.key"), 32)
		if err != nil {
			return nil, fmt.Errorf("encryption key: %w", err)
		}
		cfg.EncryptionKey = key
	}

	return cfg, nil
}

func defaults() *Config {
	return &Config{
		AdminPort:      DefaultAdminPort,
		PublicPort:     DefaultPublicPort,
		DataDir:        DefaultDataDir,
		LogLevel:       DefaultLogLevel,
		FirstRunPath:   DefaultFirstRunPath,
		CaddyEnabled:   false,
		RateLimitRate:  DefaultRateLimitRate,
		RateLimitBurst: DefaultRateLimitBurst,
	}
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("IATAN_ADMIN_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.AdminPort = p
		}
	}
	if v := os.Getenv("IATAN_PUBLIC_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cfg.PublicPort = p
		}
	}
	if v := os.Getenv("IATAN_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("IATAN_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("IATAN_JWT_SECRET"); v != "" {
		cfg.JWTSecret = v
	}
	if v := os.Getenv("IATAN_ENCRYPTION_KEY"); v != "" {
		cfg.EncryptionKey = v
	}
	if v := os.Getenv("IATAN_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("IATAN_FIRSTRUN_PATH"); v != "" {
		cfg.FirstRunPath = v
	}
	if v := os.Getenv("IATAN_CADDY_ENABLED"); v == "true" || v == "1" {
		cfg.CaddyEnabled = true
	}
}

func loadOrGenerateKey(path string, size int) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		return string(data), nil
	}

	key := make([]byte, size)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}

	hexKey := hex.EncodeToString(key)
	if err := os.WriteFile(path, []byte(hexKey), 0600); err != nil {
		return "", err
	}

	slog.Info("generated security key", "path", path)
	return hexKey, nil
}
