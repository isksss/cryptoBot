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
	appconfig "github.com/isksss/cryptoBot/internal/config"
	"github.com/isksss/cryptoBot/internal/gmo"
	"github.com/isksss/cryptoBot/internal/order"
	"github.com/isksss/cryptoBot/internal/store"
	appsync "github.com/isksss/cryptoBot/internal/sync"
)

// main は bot 実行系と管理 API を同じバイナリで起動します。
func main() {
	if err := appconfig.LoadDotEnv(".env"); err != nil {
		slog.Error("load .env", slog.Any("error", err))
		os.Exit(1)
	}

	cfg, err := appconfig.Load()
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
	gmoClient := gmo.NewClient(cfg.APIKey, cfg.APISecretKey)
	syncService := appsync.NewService(logger, queries, gmoClient)
	orderService := order.NewService(queries, gmoClient, cfg.DryRun)
	apiHandler := appapi.NewHandler(queries, dbpool, syncService, orderService, cfg.WeeklyLimitUnits)
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

	botService := bot.NewService(logger, syncService, orderService)
	errCh := make(chan error, 2)

	go func() {
		if err := botService.Run(ctx); err != nil {
			errCh <- err
		}
	}()

	go func() {
		logger.Info("http server started", slog.String("addr", cfg.HTTPAddr), slog.Bool("dryRun", cfg.DryRun))
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

// jsonRequestError は strict handler の入力エラーを JSON で返します。
func jsonRequestError(w http.ResponseWriter, _ *http.Request, err error) {
	writeJSONError(w, http.StatusBadRequest, "bad_request", err.Error())
}

// jsonResponseError は strict handler の内部エラーを JSON で返します。
func jsonResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	writeJSONError(w, http.StatusInternalServerError, "internal_error", err.Error())
}

// writeJSONError は API 全体で共通のエラーレスポンス形式を扱います。
func writeJSONError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(appapi.ErrorResponse{
		Code:    code,
		Message: message,
	})
}

// normalizeEmptyJSONBody は body なし POST を OpenAPI デコーダが扱えるよう補正します。
func normalizeEmptyJSONBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.ContentLength == 0 && r.URL.Path != "" {
			r.Body = io.NopCloser(bytes.NewBufferString("{}"))
			r.ContentLength = 2
		}

		next.ServeHTTP(w, r)
	})
}
