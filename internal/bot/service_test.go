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

func TestServiceRunCallsInitialSync(t *testing.T) {
	t.Parallel()

	syncer := &fakePriceSyncer{jobRunID: 10}
	orderSyncer := &fakeOrderSyncer{jobRunID: 11}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(logger, syncer, orderSyncer)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !syncer.called {
		t.Fatal("expected SyncPriceAndBalances to be called")
	}
	if !orderSyncer.called {
		t.Fatal("expected ReconcileOrders to be called")
	}
	if syncer.requestedBy != "startup" || syncer.reason == "" {
		t.Fatalf("unexpected sync call: requestedBy=%s reason=%s", syncer.requestedBy, syncer.reason)
	}
}

func TestServiceRunIgnoresInitialSyncError(t *testing.T) {
	t.Parallel()

	syncer := &fakePriceSyncer{err: context.DeadlineExceeded}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(logger, syncer, &fakeOrderSyncer{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}
