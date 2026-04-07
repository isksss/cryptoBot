package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/isksss/cryptoBot/internal/store"
)

func TestSyncPriceAndBalancesWithPostgres(t *testing.T) {
	databaseURL := testEnv("CRYPTOBOT_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CRYPTOBOT_DATABASE_URL is not set")
	}

	ctx := context.Background()
	dbpool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New returned error: %v", err)
	}
	defer dbpool.Close()

	if err := dbpool.Ping(ctx); err != nil {
		t.Fatalf("db ping returned error: %v", err)
	}

	if _, err := dbpool.Exec(ctx, `
		TRUNCATE TABLE trade_executions, order_events, orders, balance_snapshots, price_snapshots, job_runs RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("truncate returned error: %v", err)
	}

	queries := store.New(dbpool)
	logger := discardLogger()
	service := NewService(logger, queries, fakeGMOClient{})
	service.now = func() time.Time {
		return time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	}

	jobRunID, err := service.SyncPriceAndBalances(ctx, "integration-test", "postgres integration")
	if err != nil {
		t.Fatalf("SyncPriceAndBalances returned error: %v", err)
	}
	if jobRunID != 1 {
		t.Fatalf("unexpected jobRunID: %d", jobRunID)
	}

	var balanceCount int
	if err := dbpool.QueryRow(ctx, `SELECT COUNT(*) FROM balance_snapshots`).Scan(&balanceCount); err != nil {
		t.Fatalf("count balance_snapshots returned error: %v", err)
	}
	if balanceCount != 3 {
		t.Fatalf("unexpected balance snapshot count: %d", balanceCount)
	}

	var priceCount int
	if err := dbpool.QueryRow(ctx, `SELECT COUNT(*) FROM price_snapshots`).Scan(&priceCount); err != nil {
		t.Fatalf("count price_snapshots returned error: %v", err)
	}
	if priceCount != 2 {
		t.Fatalf("unexpected price snapshot count: %d", priceCount)
	}

	var jobStatus string
	if err := dbpool.QueryRow(ctx, `SELECT status FROM job_runs WHERE id = $1`, jobRunID).Scan(&jobStatus); err != nil {
		t.Fatalf("select job status returned error: %v", err)
	}
	if jobStatus != "succeeded" {
		t.Fatalf("unexpected job status: %s", jobStatus)
	}
}

func testEnv(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	for _, path := range []string{".env", filepath.Join("..", "..", ".env")} {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok || k != key {
				continue
			}
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}
