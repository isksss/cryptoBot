package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/order"
	"github.com/isksss/cryptoBot/internal/store"
)

type fakeQuerier struct {
	balances        []store.ListLatestBalancesRow
	prices          []store.ListLatestPricesRow
	orders          []store.ListOrdersRow
	jobRuns         []store.ListLatestJobRunsRow
	openCount       int64
	unresolvedCount int64
	weeklyConsumed  []store.ListWeeklyConsumedBuyUnitsRow
}

func (f *fakeQuerier) CountJobRunsByTypeInWindow(context.Context, store.CountJobRunsByTypeInWindowParams) (int64, error) {
	return 0, nil
}
func (f *fakeQuerier) CountOpenOrders(context.Context) (int64, error) { return f.openCount, nil }
func (f *fakeQuerier) CountUnresolvedPreviousDayOrders(context.Context) (int64, error) {
	return f.unresolvedCount, nil
}
func (f *fakeQuerier) GetOrder(context.Context, int64) (store.GetOrderRow, error) {
	return store.GetOrderRow{}, pgx.ErrNoRows
}
func (f *fakeQuerier) InsertBalanceSnapshot(context.Context, store.InsertBalanceSnapshotParams) (store.InsertBalanceSnapshotRow, error) {
	return store.InsertBalanceSnapshotRow{}, nil
}
func (f *fakeQuerier) InsertJobRun(context.Context, store.InsertJobRunParams) (store.InsertJobRunRow, error) {
	return store.InsertJobRunRow{}, nil
}
func (f *fakeQuerier) InsertOrder(context.Context, store.InsertOrderParams) (store.InsertOrderRow, error) {
	return store.InsertOrderRow{}, nil
}
func (f *fakeQuerier) InsertOrderEvent(context.Context, store.InsertOrderEventParams) error { return nil }
func (f *fakeQuerier) InsertPriceSnapshot(context.Context, store.InsertPriceSnapshotParams) (store.InsertPriceSnapshotRow, error) {
	return store.InsertPriceSnapshotRow{}, nil
}
func (f *fakeQuerier) InsertTradeExecution(context.Context, store.InsertTradeExecutionParams) error { return nil }
func (f *fakeQuerier) ListExecutions(context.Context, store.ListExecutionsParams) ([]store.ListExecutionsRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListJobRuns(context.Context, store.ListJobRunsParams) ([]store.ListJobRunsRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListLatestBalances(context.Context) ([]store.ListLatestBalancesRow, error) {
	return f.balances, nil
}
func (f *fakeQuerier) ListLatestJobRuns(context.Context, int32) ([]store.ListLatestJobRunsRow, error) {
	return f.jobRuns, nil
}
func (f *fakeQuerier) ListLatestPrices(context.Context, *string) ([]store.ListLatestPricesRow, error) {
	return f.prices, nil
}
func (f *fakeQuerier) ListOrderEventsByOrderID(context.Context, int64) ([]store.ListOrderEventsByOrderIDRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListOrders(context.Context, store.ListOrdersParams) ([]store.ListOrdersRow, error) {
	return f.orders, nil
}
func (f *fakeQuerier) ListPriceHistory(context.Context, store.ListPriceHistoryParams) ([]store.ListPriceHistoryRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListReconcilableOrders(context.Context, int32) ([]store.ListReconcilableOrdersRow, error) {
	return nil, nil
}
func (f *fakeQuerier) ListWeeklyConsumedBuyUnits(context.Context, pgtype.Timestamptz) ([]store.ListWeeklyConsumedBuyUnitsRow, error) {
	return f.weeklyConsumed, nil
}
func (f *fakeQuerier) MarkJobRunFailed(context.Context, store.MarkJobRunFailedParams) error { return nil }
func (f *fakeQuerier) MarkJobRunSkipped(context.Context, store.MarkJobRunSkippedParams) error {
	return nil
}
func (f *fakeQuerier) MarkJobRunSucceeded(context.Context, store.MarkJobRunSucceededParams) error {
	return nil
}
func (f *fakeQuerier) MarkOrderCancelRequested(context.Context, store.MarkOrderCancelRequestedParams) error {
	return nil
}
func (f *fakeQuerier) MarkOrderCancelled(context.Context, store.MarkOrderCancelledParams) error { return nil }
func (f *fakeQuerier) UpdateOrderAfterSync(context.Context, store.UpdateOrderAfterSyncParams) error {
	return nil
}

type fakePriceSyncer struct{ called bool }

func (f *fakePriceSyncer) SyncPriceAndBalances(context.Context, string, string) (int64, error) {
	f.called = true
	return 1, nil
}

type fakeOrderService struct {
	created   order.CreateInput
	cancelled int64
}

func (f *fakeOrderService) CreateLimitOrder(_ context.Context, input order.CreateInput) (store.InsertOrderRow, error) {
	f.created = input
	return store.InsertOrderRow{ID: 1, ClientOrderID: pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}}, nil
}
func (f *fakeOrderService) CancelOrder(_ context.Context, localOrderID int64) error {
	f.cancelled = localOrderID
	return nil
}
func (f *fakeOrderService) DailyTrade(context.Context, string, string) (int64, error) { return 1, nil }
func (f *fakeOrderService) ReconcileOrders(context.Context, string, string) (int64, error) {
	return 1, nil
}

func TestIndex(t *testing.T) {
	t.Parallel()

	h, err := NewHandler(&fakeQuerier{}, &fakePriceSyncer{}, &fakeOrderService{}, "0.1", true)
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CryptoBot Console") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestCreateOrderAction(t *testing.T) {
	t.Parallel()

	orderSvc := &fakeOrderService{}
	h, err := NewHandler(&fakeQuerier{}, &fakePriceSyncer{}, orderSvc, "0.1", true)
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}

	form := strings.NewReader("assetCode=BTC&side=buy&priceJpy=10000000&units=0.001&timeInForce=SOK&requestedBy=web-ui")
	req := httptest.NewRequest(http.MethodPost, "/ui/actions/orders", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if orderSvc.created.AssetCode != "BTC" || orderSvc.created.Units != "0.001" {
		t.Fatalf("unexpected create input: %+v", orderSvc.created)
	}
}

func TestOrdersPartial(t *testing.T) {
	t.Parallel()

	h, err := NewHandler(&fakeQuerier{
		orders: []store.ListOrdersRow{{
			ID:           10,
			AssetCode:    "BTC",
			Side:         "buy",
			PriceJpy:     "10000000",
			OrderedUnits: "0.001",
			Status:       "open",
			PlacedAt:     pgTS(time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)),
		}},
	}, &fakePriceSyncer{}, &fakeOrderService{}, "0.1", true)
	if err != nil {
		t.Fatalf("NewHandler returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ui/partials/orders", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "BTC") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func pgTS(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
