package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	appapi "github.com/isksss/cryptoBot/internal/api"
	"github.com/isksss/cryptoBot/internal/bot"
	"github.com/isksss/cryptoBot/internal/store"
)

type config struct {
	DatabaseURL      string
	HTTPAddr         string
	WeeklyLimitUnits string
	LogLevel         slog.Level
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("load config", slog.Any("error", err))
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	dbpool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", slog.Any("error", err))
		os.Exit(1)
	}
	defer dbpool.Close()

	queries := store.New(dbpool)
	apiHandler := appapi.NewHandler(queries, dbpool, cfg.WeeklyLimitUnits)
	serverInterface := appapi.NewStrictHandlerWithOptions(apiHandler, nil, appapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  jsonRequestError,
		ResponseErrorHandlerFunc: jsonResponseError,
	})

	mux := http.NewServeMux()
	httpHandler := appapi.HandlerFromMux(serverInterface, mux)
	httpHandler = normalizeEmptyJSONBody(httpHandler)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	botService := bot.NewService(logger)
	errCh := make(chan error, 2)

	go func() {
		if err := botService.Run(ctx); err != nil {
			errCh <- err
		}
	}()

	go func() {
		logger.Info("http server started", slog.String("addr", cfg.HTTPAddr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("service stopped with error", slog.Any("error", err))
		cancel()
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("http server shutdown", slog.Any("error", err))
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	cfg := config{
		DatabaseURL:      os.Getenv("CRYPTOBOT_DATABASE_URL"),
		HTTPAddr:         envOrDefault("CRYPTOBOT_HTTP_ADDR", ":8080"),
		WeeklyLimitUnits: envOrDefault("CRYPTOBOT_WEEKLY_LIMIT_UNITS", "0"),
		LogLevel:         parseLogLevel(envOrDefault("CRYPTOBOT_LOG_LEVEL", "info")),
	}

	if cfg.DatabaseURL == "" {
		return config{}, errors.New("CRYPTOBOT_DATABASE_URL is required")
	}

	return cfg, nil
}

func envOrDefault(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func parseLogLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}

func jsonRequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
}

func jsonResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	writeJSONError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

func writeJSONError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(appapi.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

func normalizeEmptyJSONBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.ContentLength == 0 && r.URL.Path != "" {
			r.Body = io.NopCloser(bytes.NewBufferString("{}"))
			r.ContentLength = 2
		}

		next.ServeHTTP(w, r)
	})
}
