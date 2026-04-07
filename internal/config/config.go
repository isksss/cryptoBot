package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// App はアプリ全体で共有する実行時設定です。
type App struct {
	DatabaseURL      string
	HTTPAddr         string
	WeeklyLimitUnits string
	APIKey           string
	APISecretKey     string
	DryRun           bool
	PriceSyncInterval time.Duration
	OrderReconcileInterval time.Duration
	LogLevel         slog.Level
}

// Load は環境変数から設定を読み出し、必須値を検証します。
func Load() (App, error) {
	dryRun, err := ParseBool(EnvOrDefault("CRYPTOBOT_DRY_RUN", "false"))
	if err != nil {
		return App{}, fmt.Errorf("CRYPTOBOT_DRY_RUN の解釈に失敗: %w", err)
	}
	priceSyncInterval, err := time.ParseDuration(EnvOrDefault("CRYPTOBOT_PRICE_SYNC_INTERVAL", "1h"))
	if err != nil {
		return App{}, fmt.Errorf("CRYPTOBOT_PRICE_SYNC_INTERVAL の解釈に失敗: %w", err)
	}
	orderReconcileInterval, err := time.ParseDuration(EnvOrDefault("CRYPTOBOT_ORDER_RECONCILE_INTERVAL", "5m"))
	if err != nil {
		return App{}, fmt.Errorf("CRYPTOBOT_ORDER_RECONCILE_INTERVAL の解釈に失敗: %w", err)
	}

	cfg := App{
		DatabaseURL:      os.Getenv("CRYPTOBOT_DATABASE_URL"),
		HTTPAddr:         EnvOrDefault("CRYPTOBOT_HTTP_ADDR", ":8080"),
		WeeklyLimitUnits: EnvOrDefault("CRYPTOBOT_WEEKLY_LIMIT_UNITS", "0"),
		APIKey:           os.Getenv("CRYPTOBOT_API_KEY"),
		APISecretKey:     os.Getenv("CRYPTOBOT_API_SECRET_KEY"),
		DryRun:           dryRun,
		PriceSyncInterval: priceSyncInterval,
		OrderReconcileInterval: orderReconcileInterval,
		LogLevel:         ParseLogLevel(EnvOrDefault("CRYPTOBOT_LOG_LEVEL", "info")),
	}

	if cfg.DatabaseURL == "" {
		return App{}, errors.New("CRYPTOBOT_DATABASE_URL is required")
	}
	if cfg.APIKey == "" {
		return App{}, errors.New("CRYPTOBOT_API_KEY is required")
	}
	if cfg.APISecretKey == "" {
		return App{}, errors.New("CRYPTOBOT_API_SECRET_KEY is required")
	}

	return cfg, nil
}

// EnvOrDefault は環境変数が未設定ならデフォルト値を返します。
func EnvOrDefault(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// ParseLogLevel は文字列設定を slog のレベルに変換します。
func ParseLogLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}

// ParseBool は環境変数向けの真偽値を解釈します。
func ParseBool(value string) (bool, error) {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, err
	}
	return parsed, nil
}
