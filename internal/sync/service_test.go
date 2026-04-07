package sync

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/gmo"
	"github.com/isksss/cryptoBot/internal/store"
)

type fakeQueries struct {
	insertedJobRun           store.InsertJobRunParams
	markedSucceeded          *store.MarkJobRunSucceededParams
	markedFailed             *store.MarkJobRunFailedParams
	insertedBalanceSnapshots []store.InsertBalanceSnapshotParams
	insertedPriceSnapshots   []store.InsertPriceSnapshotParams
}

func (f *fakeQueries) InsertJobRun(_ context.Context, arg store.InsertJobRunParams) (store.InsertJobRunRow, error) {
	f.insertedJobRun = arg
	return store.InsertJobRunRow{ID: 42}, nil
}

func (f *fakeQueries) MarkJobRunFailed(_ context.Context, arg store.MarkJobRunFailedParams) error {
	f.markedFailed = &arg
	return nil
}

func (f *fakeQueries) MarkJobRunSucceeded(_ context.Context, arg store.MarkJobRunSucceededParams) error {
	f.markedSucceeded = &arg
	return nil
}

func (f *fakeQueries) InsertBalanceSnapshot(_ context.Context, arg store.InsertBalanceSnapshotParams) (store.InsertBalanceSnapshotRow, error) {
	f.insertedBalanceSnapshots = append(f.insertedBalanceSnapshots, arg)
	return store.InsertBalanceSnapshotRow{}, nil
}

func (f *fakeQueries) InsertPriceSnapshot(_ context.Context, arg store.InsertPriceSnapshotParams) (store.InsertPriceSnapshotRow, error) {
	f.insertedPriceSnapshots = append(f.insertedPriceSnapshots, arg)
	return store.InsertPriceSnapshotRow{}, nil
}

type fakeGMOClient struct{}

func (fakeGMOClient) GetAssets(context.Context) ([]gmo.Asset, error) {
	return []gmo.Asset{
		{Symbol: "JPY", Amount: "100000.00", Available: "90000.00"},
		{Symbol: "BTC", Amount: "0.20000000", Available: "0.15000000"},
		{Symbol: "ETH", Amount: "1.50000000", Available: "1.00000000"},
	}, nil
}

func (fakeGMOClient) GetTicker(_ context.Context, symbol string) (gmo.Ticker, error) {
	values := map[string]string{
		"BTC": "10000000",
		"ETH": "300000",
	}
	return gmo.Ticker{
		Symbol:    symbol,
		Last:      values[symbol],
		Timestamp: time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC),
	}, nil
}

func TestSyncPriceAndBalances(t *testing.T) {
	t.Parallel()

	queries := &fakeQueries{}
	logger := discardLogger()
	service := NewService(logger, queries, fakeGMOClient{})
	service.now = func() time.Time {
		return time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	}

	jobRunID, err := service.SyncPriceAndBalances(context.Background(), "test", "unit test")
	if err != nil {
		t.Fatalf("SyncPriceAndBalances returned error: %v", err)
	}
	if jobRunID != 42 {
		t.Fatalf("unexpected jobRunID: %d", jobRunID)
	}
	if queries.markedSucceeded == nil {
		t.Fatal("expected job run to be marked as succeeded")
	}
	if queries.markedFailed != nil {
		t.Fatal("did not expect job run to be marked as failed")
	}
	if len(queries.insertedBalanceSnapshots) != 3 {
		t.Fatalf("unexpected balance snapshot count: %d", len(queries.insertedBalanceSnapshots))
	}
	if len(queries.insertedPriceSnapshots) != 2 {
		t.Fatalf("unexpected price snapshot count: %d", len(queries.insertedPriceSnapshots))
	}

	btcBalance := queries.insertedBalanceSnapshots[1]
	if btcBalance.AssetCode != "BTC" {
		t.Fatalf("unexpected balance asset code: %s", btcBalance.AssetCode)
	}
	if got := numericString(t, btcBalance.LockedAmount); got != "0.05000000" {
		t.Fatalf("unexpected BTC locked amount: %s", got)
	}

	btcPrice := queries.insertedPriceSnapshots[0]
	if btcPrice.AssetCode != "BTC" {
		t.Fatalf("unexpected price asset code: %s", btcPrice.AssetCode)
	}
	if got := numericString(t, btcPrice.PriceJpy); got != "10000000.00000000" {
		t.Fatalf("unexpected BTC price: %s", got)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func numericString(t *testing.T, n pgtype.Numeric) string {
	t.Helper()
	r, err := numericToRat(n)
	if err != nil {
		t.Fatalf("numericToRat returned error: %v", err)
	}
	return r.FloatString(8)
}
