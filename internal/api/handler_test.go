package api

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/order"
	"github.com/isksss/cryptoBot/internal/store"
)

type fakeQuerier struct {
	balances          []store.ListLatestBalancesRow
	prices            []store.ListLatestPricesRow
	jobRuns           []store.ListLatestJobRunsRow
	openCount         int64
	unresolvedCount   int64
	weeklyConsumed    []store.ListWeeklyConsumedBuyUnitsRow
	insertJobRunRow   store.InsertJobRunRow
	insertJobRunError error
}

func (f *fakeQuerier) CountOpenOrders(context.Context) (int64, error) { return f.openCount, nil }
func (f *fakeQuerier) CountJobRunsByTypeInWindow(context.Context, store.CountJobRunsByTypeInWindowParams) (int64, error) {
	return 0, nil
}
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
	return f.insertJobRunRow, f.insertJobRunError
}
func (f *fakeQuerier) InsertOrder(context.Context, store.InsertOrderParams) (store.InsertOrderRow, error) {
	return store.InsertOrderRow{}, nil
}
func (f *fakeQuerier) InsertOrderEvent(context.Context, store.InsertOrderEventParams) error { return nil }
func (f *fakeQuerier) InsertTradeExecution(context.Context, store.InsertTradeExecutionParams) error {
	return nil
}
func (f *fakeQuerier) InsertPriceSnapshot(context.Context, store.InsertPriceSnapshotParams) (store.InsertPriceSnapshotRow, error) {
	return store.InsertPriceSnapshotRow{}, nil
}
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
	return nil, nil
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
func (f *fakeQuerier) MarkJobRunSucceeded(context.Context, store.MarkJobRunSucceededParams) error {
	return nil
}
func (f *fakeQuerier) MarkJobRunSkipped(context.Context, store.MarkJobRunSkippedParams) error { return nil }
func (f *fakeQuerier) MarkOrderCancelRequested(context.Context, store.MarkOrderCancelRequestedParams) error {
	return nil
}
func (f *fakeQuerier) MarkOrderCancelled(context.Context, store.MarkOrderCancelledParams) error { return nil }
func (f *fakeQuerier) UpdateOrderAfterSync(context.Context, store.UpdateOrderAfterSyncParams) error {
	return nil
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type fakeSyncer struct {
	jobRunID    int64
	err         error
	requestedBy string
	reason      string
}

func (f *fakeSyncer) SyncPriceAndBalances(_ context.Context, requestedBy string, reason string) (int64, error) {
	f.requestedBy = requestedBy
	f.reason = reason
	return f.jobRunID, f.err
}

type fakeOrderService struct {
	row            store.InsertOrderRow
	createErr      error
	cancelErr      error
	dailyTradeErr  error
	dailyTradeJobID int64
	reconcileErr   error
	reconcileJobID int64
	lastCreate     order.CreateInput
	lastCancelled  int64
	lastRequestedBy string
	lastReason      string
}

func (f *fakeOrderService) CreateLimitOrder(_ context.Context, input order.CreateInput) (store.InsertOrderRow, error) {
	f.lastCreate = input
	return f.row, f.createErr
}

func (f *fakeOrderService) CancelOrder(_ context.Context, localOrderID int64) error {
	f.lastCancelled = localOrderID
	return f.cancelErr
}

func (f *fakeOrderService) DailyTrade(_ context.Context, requestedBy string, reason string) (int64, error) {
	f.lastRequestedBy = requestedBy
	f.lastReason = reason
	return f.dailyTradeJobID, f.dailyTradeErr
}

func (f *fakeOrderService) ReconcileOrders(_ context.Context, requestedBy string, reason string) (int64, error) {
	f.lastRequestedBy = requestedBy
	f.lastReason = reason
	return f.reconcileJobID, f.reconcileErr
}

func TestGetLatestBalancesFiltersByAssetCode(t *testing.T) {
	t.Parallel()

	queries := &fakeQuerier{
		balances: []store.ListLatestBalancesRow{
			{AssetCode: "JPY", AvailableAmount: "100", LockedAmount: "0"},
			{AssetCode: "BTC", AvailableAmount: "0.1", LockedAmount: "0.01"},
		},
	}
	server := NewHandler(queries, fakePinger{}, nil, nil, "1")

	asset := BalanceAssetCodeOptional(BalanceAssetCodeBTC)
	resp, err := server.GetLatestBalances(context.Background(), GetLatestBalancesRequestObject{
		Params: GetLatestBalancesParams{AssetCode: &asset},
	})
	if err != nil {
		t.Fatalf("GetLatestBalances returned error: %v", err)
	}

	body := resp.(GetLatestBalances200JSONResponse)
	if len(body.Balances) != 1 {
		t.Fatalf("unexpected balances length: %d", len(body.Balances))
	}
	if body.Balances[0].AssetCode != BalanceAssetCodeBTC {
		t.Fatalf("unexpected asset code: %s", body.Balances[0].AssetCode)
	}
}

func TestTriggerPriceFetchJob(t *testing.T) {
	t.Parallel()

	syncer := &fakeSyncer{jobRunID: 77}
	server := NewHandler(&fakeQuerier{}, fakePinger{}, syncer, nil, "1")
	requestedBy := "tester"
	reason := "manual"

	resp, err := server.TriggerPriceFetchJob(context.Background(), TriggerPriceFetchJobRequestObject{
		Body: &TriggerJobRequest{RequestedBy: &requestedBy, Reason: &reason},
	})
	if err != nil {
		t.Fatalf("TriggerPriceFetchJob returned error: %v", err)
	}

	body := resp.(TriggerPriceFetchJob202JSONResponse)
	if body.JobRunId != 77 {
		t.Fatalf("unexpected job run id: %d", body.JobRunId)
	}
	if syncer.requestedBy != requestedBy || syncer.reason != reason {
		t.Fatalf("unexpected syncer args: %+v", syncer)
	}
}

func TestGetOrderReturnsNotFound(t *testing.T) {
	t.Parallel()

	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, nil, "1")
	resp, err := server.GetOrder(context.Background(), GetOrderRequestObject{OrderId: 1})
	if err != nil {
		t.Fatalf("GetOrder returned error: %v", err)
	}

	notFound := resp.(GetOrder404JSONResponse)
	if notFound.Code != "not_found" {
		t.Fatalf("unexpected error code: %s", notFound.Code)
	}
}

func TestGetHealth(t *testing.T) {
	t.Parallel()

	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, nil, "1")
	server.now = func() time.Time { return time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC) }

	resp, err := server.GetHealth(context.Background(), GetHealthRequestObject{})
	if err != nil {
		t.Fatalf("GetHealth returned error: %v", err)
	}

	body := resp.(GetHealth200JSONResponse)
	if body.Status != "ok" || body.CheckedAt.IsZero() {
		t.Fatalf("unexpected health response: %+v", body)
	}
}

func TestGetSystemSummary(t *testing.T) {
	t.Parallel()

	queries := &fakeQuerier{
		balances: []store.ListLatestBalancesRow{
			{AssetCode: "BTC", AvailableAmount: "0.1", LockedAmount: "0.01"},
			{AssetCode: "JPY", AvailableAmount: "100", LockedAmount: "10"},
		},
		jobRuns: []store.ListLatestJobRunsRow{
			{
				ID:           1,
				JobType:      "price_fetch",
				Status:       "succeeded",
				ScheduledFor: pgTS(time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)),
				StartedAt:    pgTS(time.Date(2026, 4, 7, 0, 0, 1, 0, time.UTC)),
			},
		},
		openCount:       2,
		unresolvedCount: 1,
		weeklyConsumed: []store.ListWeeklyConsumedBuyUnitsRow{
			{AssetCode: "BTC", ConsumedUnits: "0.02000000"},
		},
	}
	server := NewHandler(queries, fakePinger{}, nil, nil, "0.10000000")
	server.now = func() time.Time { return time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC) }

	resp, err := server.GetSystemSummary(context.Background(), GetSystemSummaryRequestObject{})
	if err != nil {
		t.Fatalf("GetSystemSummary returned error: %v", err)
	}

	body := resp.(GetSystemSummary200JSONResponse)
	if body.OrderStats.OpenCount != 2 || body.OrderStats.UnresolvedPreviousDayCount != 1 {
		t.Fatalf("unexpected order stats: %+v", body.OrderStats)
	}
	if len(body.Balances) != 2 || len(body.WeeklyLimits) != 2 {
		t.Fatalf("unexpected summary lengths: balances=%d weekly=%d", len(body.Balances), len(body.WeeklyLimits))
	}
	if body.WeeklyLimits[0].RemainingUnits != "0.08000000" {
		t.Fatalf("unexpected remaining units: %s", body.WeeklyLimits[0].RemainingUnits)
	}
}

func TestCreateOrder(t *testing.T) {
	t.Parallel()

	clientID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	orderSvc := &fakeOrderService{
		row: store.InsertOrderRow{
			ID:                  10,
			ExchangeOrderID:     "999",
			ClientOrderID:       pgtype.UUID{Bytes: clientID, Valid: true},
			AssetCode:           "BTC",
			Side:                "buy",
			OrderType:           "limit",
			Status:              "open",
			PriceJpy:            "10000000.00000000",
			OrderedUnits:        "0.01000000",
			FilledUnits:         "0.00000000",
			RemainingUnits:      "0.01000000",
			FeeJpy:              "0.00000000",
			IsFeeFree:           true,
			PlacedAt:            pgTS(time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)),
			LastStatusCheckedAt: pgTS(time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)),
		},
	}
	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, orderSvc, "1")
	requestedBy := "tester"
	timeInForce := SOK

	resp, err := server.CreateOrder(context.Background(), CreateOrderRequestObject{
		Body: &CreateOrderJSONRequestBody{
			AssetCode:   AssetCodeBTC,
			Side:        Buy,
			PriceJpy:    "10000000",
			Units:       "0.01",
			TimeInForce: &timeInForce,
			RequestedBy: &requestedBy,
		},
	})
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}

	body := resp.(CreateOrder201JSONResponse)
	if body.Order.Id != 10 || body.Order.Status != OrderStatusOpen {
		t.Fatalf("unexpected order response: %+v", body.Order)
	}
	if orderSvc.lastCreate.AssetCode != "BTC" || orderSvc.lastCreate.TimeInForce != "SOK" {
		t.Fatalf("unexpected create input: %+v", orderSvc.lastCreate)
	}
}

func TestCancelOrderNotFound(t *testing.T) {
	t.Parallel()

	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, &fakeOrderService{cancelErr: pgx.ErrNoRows}, "1")
	resp, err := server.CancelOrder(context.Background(), CancelOrderRequestObject{OrderId: 99})
	if err != nil {
		t.Fatalf("CancelOrder returned error: %v", err)
	}

	body := resp.(CancelOrder404JSONResponse)
	if body.Code != "not_found" {
		t.Fatalf("unexpected error code: %s", body.Code)
	}
}

func TestCancelOrder(t *testing.T) {
	t.Parallel()

	orderSvc := &fakeOrderService{}
	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, orderSvc, "1")
	resp, err := server.CancelOrder(context.Background(), CancelOrderRequestObject{OrderId: 12})
	if err != nil {
		t.Fatalf("CancelOrder returned error: %v", err)
	}

	body := resp.(CancelOrder200JSONResponse)
	if body.OrderId != 12 || body.Status != OrderStatusCancelled {
		t.Fatalf("unexpected cancel response: %+v", body)
	}
	if orderSvc.lastCancelled != 12 {
		t.Fatalf("unexpected cancelled id: %d", orderSvc.lastCancelled)
	}
}

func TestTriggerOrderReconcileJob(t *testing.T) {
	t.Parallel()

	orderSvc := &fakeOrderService{reconcileJobID: 88}
	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, orderSvc, "1")
	requestedBy := "tester"
	reason := "manual reconcile"

	resp, err := server.TriggerOrderReconcileJob(context.Background(), TriggerOrderReconcileJobRequestObject{
		Body: &TriggerJobRequest{RequestedBy: &requestedBy, Reason: &reason},
	})
	if err != nil {
		t.Fatalf("TriggerOrderReconcileJob returned error: %v", err)
	}

	body := resp.(TriggerOrderReconcileJob202JSONResponse)
	if body.JobRunId != 88 {
		t.Fatalf("unexpected job run id: %d", body.JobRunId)
	}
	if orderSvc.lastRequestedBy != requestedBy || orderSvc.lastReason != reason {
		t.Fatalf("unexpected reconcile args: %+v", orderSvc)
	}
}

func TestTriggerDailyTradeJob(t *testing.T) {
	t.Parallel()

	orderSvc := &fakeOrderService{dailyTradeJobID: 89}
	server := NewHandler(&fakeQuerier{}, fakePinger{}, nil, orderSvc, "1")
	requestedBy := "tester"
	reason := "manual daily trade"

	resp, err := server.TriggerDailyTradeJob(context.Background(), TriggerDailyTradeJobRequestObject{
		Body: &TriggerJobRequest{RequestedBy: &requestedBy, Reason: &reason},
	})
	if err != nil {
		t.Fatalf("TriggerDailyTradeJob returned error: %v", err)
	}

	body := resp.(TriggerDailyTradeJob202JSONResponse)
	if body.JobRunId != 89 {
		t.Fatalf("unexpected job run id: %d", body.JobRunId)
	}
	if orderSvc.lastRequestedBy != requestedBy || orderSvc.lastReason != reason {
		t.Fatalf("unexpected daily trade args: %+v", orderSvc)
	}
}

func pgTS(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TestDiscardLoggerSanity(t *testing.T) {
	t.Parallel()
	_ = slog.New(slog.NewTextHandler(io.Discard, nil))
}
