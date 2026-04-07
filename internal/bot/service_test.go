package bot

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakePriceSyncer struct {
	called      bool
	requestedBy string
	reason      string
	jobRunID    int64
	err         error
}

func (f *fakePriceSyncer) SyncPriceAndBalances(_ context.Context, requestedBy string, reason string) (int64, error) {
	f.called = true
	f.requestedBy = requestedBy
	f.reason = reason
	return f.jobRunID, f.err
}

type fakeOrderSyncer struct {
	called      bool
	requestedBy string
	reason      string
	jobRunID    int64
	err         error
}

func (f *fakeOrderSyncer) ReconcileOrders(_ context.Context, requestedBy string, reason string) (int64, error) {
	f.called = true
	f.requestedBy = requestedBy
	f.reason = reason
	return f.jobRunID, f.err
}

type fakeDailyTrader struct {
	called      bool
	requestedBy string
	reason      string
	jobRunID    int64
	err         error
}

func (f *fakeDailyTrader) DailyTrade(_ context.Context, requestedBy string, reason string) (int64, error) {
	f.called = true
	f.requestedBy = requestedBy
	f.reason = reason
	return f.jobRunID, f.err
}

func TestServiceRunCallsInitialSync(t *testing.T) {
	t.Parallel()

	priceSyncer := &fakePriceSyncer{jobRunID: 10}
	orderSyncer := &fakeOrderSyncer{jobRunID: 11}
	dailyTrader := &fakeDailyTrader{jobRunID: 12}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(logger, priceSyncer, orderSyncer, dailyTrader, time.Hour, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !priceSyncer.called {
		t.Fatal("expected SyncPriceAndBalances to be called")
	}
	if !orderSyncer.called {
		t.Fatal("expected ReconcileOrders to be called")
	}
	if dailyTrader.called {
		t.Fatal("did not expect DailyTrade to run immediately")
	}
}

func TestNextJSTMidnight(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 8, 12, 34, 56, 0, time.FixedZone("UTC+0", 0))
	next := nextJSTMidnight(now)
	jst := time.FixedZone("Asia/Tokyo", 9*60*60)
	inJST := next.In(jst)
	if inJST.Hour() != 0 || inJST.Minute() != 0 || inJST.Day() != 9 {
		t.Fatalf("unexpected next midnight: %s", inJST)
	}
}
