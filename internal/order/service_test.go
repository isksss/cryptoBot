package order

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/gmo"
	"github.com/isksss/cryptoBot/internal/store"
)

type fakeStore struct {
	insertJobRunParams       store.InsertJobRunParams
	insertJobRunRow          store.InsertJobRunRow
	markJobRunSucceededParam *store.MarkJobRunSucceededParams
	markJobRunFailedParam    *store.MarkJobRunFailedParams
	insertOrderParams        store.InsertOrderParams
	insertOrderRow           store.InsertOrderRow
	insertOrderErr           error
	insertedOrderEvents      []store.InsertOrderEventParams
	getOrderRow              store.GetOrderRow
	getOrderErr              error
	reconcilableOrders       []store.ListReconcilableOrdersRow
	updateOrderAfterSync     *store.UpdateOrderAfterSyncParams
	insertedTradeExecutions  []store.InsertTradeExecutionParams
	cancelRequestedParams    *store.MarkOrderCancelRequestedParams
	cancelledParams          *store.MarkOrderCancelledParams
}

func (f *fakeStore) InsertJobRun(_ context.Context, arg store.InsertJobRunParams) (store.InsertJobRunRow, error) {
	f.insertJobRunParams = arg
	if f.insertJobRunRow.ID == 0 {
		f.insertJobRunRow.ID = 1
	}
	return f.insertJobRunRow, nil
}

func (f *fakeStore) MarkJobRunFailed(_ context.Context, arg store.MarkJobRunFailedParams) error {
	f.markJobRunFailedParam = &arg
	return nil
}

func (f *fakeStore) MarkJobRunSucceeded(_ context.Context, arg store.MarkJobRunSucceededParams) error {
	f.markJobRunSucceededParam = &arg
	return nil
}

func (f *fakeStore) InsertOrder(_ context.Context, arg store.InsertOrderParams) (store.InsertOrderRow, error) {
	f.insertOrderParams = arg
	return f.insertOrderRow, f.insertOrderErr
}

func (f *fakeStore) InsertOrderEvent(_ context.Context, arg store.InsertOrderEventParams) error {
	f.insertedOrderEvents = append(f.insertedOrderEvents, arg)
	return nil
}

func (f *fakeStore) GetOrder(_ context.Context, id int64) (store.GetOrderRow, error) {
	if f.getOrderErr != nil {
		return store.GetOrderRow{}, f.getOrderErr
	}
	row := f.getOrderRow
	row.ID = id
	return row, nil
}

func (f *fakeStore) ListReconcilableOrders(_ context.Context, _ int32) ([]store.ListReconcilableOrdersRow, error) {
	return f.reconcilableOrders, nil
}

func (f *fakeStore) UpdateOrderAfterSync(_ context.Context, arg store.UpdateOrderAfterSyncParams) error {
	f.updateOrderAfterSync = &arg
	return nil
}

func (f *fakeStore) InsertTradeExecution(_ context.Context, arg store.InsertTradeExecutionParams) error {
	f.insertedTradeExecutions = append(f.insertedTradeExecutions, arg)
	return nil
}

func (f *fakeStore) MarkOrderCancelRequested(_ context.Context, arg store.MarkOrderCancelRequestedParams) error {
	f.cancelRequestedParams = &arg
	return nil
}

func (f *fakeStore) MarkOrderCancelled(_ context.Context, arg store.MarkOrderCancelledParams) error {
	f.cancelledParams = &arg
	return nil
}

type fakeExchangeClient struct {
	rules              []gmo.SymbolRule
	ordersByBatch      []gmo.Order
	executionsByOrder  map[int64][]gmo.Execution
	createReq          gmo.CreateOrderRequest
	createResp         gmo.CreateOrderResponse
	createErr          error
	cancelOrderID      int64
	cancelErr          error
}

func (f *fakeExchangeClient) GetSymbolRules(context.Context) ([]gmo.SymbolRule, error) {
	return f.rules, nil
}

func (f *fakeExchangeClient) GetOrders(_ context.Context, _ []int64) ([]gmo.Order, error) {
	return f.ordersByBatch, nil
}

func (f *fakeExchangeClient) GetExecutions(_ context.Context, orderID int64) ([]gmo.Execution, error) {
	return f.executionsByOrder[orderID], nil
}

func (f *fakeExchangeClient) CreateOrder(_ context.Context, reqBody gmo.CreateOrderRequest) (gmo.CreateOrderResponse, error) {
	f.createReq = reqBody
	return f.createResp, f.createErr
}

func (f *fakeExchangeClient) CancelOrder(_ context.Context, orderID int64) error {
	f.cancelOrderID = orderID
	return f.cancelErr
}

func TestCreateLimitOrder(t *testing.T) {
	t.Parallel()

	clientID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	storeStub := &fakeStore{
		insertOrderRow: store.InsertOrderRow{
			ID:                  1,
			ExchangeOrderID:     "12345",
			ClientOrderID:       pgtype.UUID{Bytes: clientID, Valid: true},
			AssetCode:           "BTC",
			Side:                "buy",
			OrderType:           "limit",
			Status:              "open",
			PriceJpy:            "10000000.00000000",
			OrderedUnits:        "0.00100000",
			FilledUnits:         "0.00000000",
			RemainingUnits:      "0.00100000",
			FeeJpy:              "0.00000000",
			IsFeeFree:           true,
			PlacedAt:            pgTime(time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)),
			LastStatusCheckedAt: pgTime(time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)),
		},
	}
	exchangeStub := &fakeExchangeClient{
		rules: []gmo.SymbolRule{{Symbol: "BTC_JPY", MinOrderSize: "0.001", SizeStep: "0.001"}},
		createResp: gmo.CreateOrderResponse{OrderID: "12345"},
	}

	service := NewService(storeStub, exchangeStub, false)
	service.now = func() time.Time { return time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC) }

	row, err := service.CreateLimitOrder(context.Background(), CreateInput{
		AssetCode:   "BTC",
		Side:        "buy",
		PriceJpy:    "10000000",
		Units:       "0.001",
		TimeInForce: "SOK",
		RequestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("CreateLimitOrder returned error: %v", err)
	}

	if row.ID != 1 {
		t.Fatalf("unexpected returned row: %+v", row)
	}
	if exchangeStub.createReq.Symbol != "BTC_JPY" || exchangeStub.createReq.ExecutionType != "LIMIT" {
		t.Fatalf("unexpected create request: %+v", exchangeStub.createReq)
	}
	if !storeStub.insertOrderParams.IsFeeFree || storeStub.insertOrderParams.Status != "open" {
		t.Fatalf("unexpected inserted order params: %+v", storeStub.insertOrderParams)
	}
}

func TestCreateLimitOrderDryRun(t *testing.T) {
	t.Parallel()

	storeStub := &fakeStore{
		insertOrderRow: store.InsertOrderRow{
			ID:                  2,
			ExchangeOrderID:     "dryrun-1",
			ClientOrderID:       pgtype.UUID{Bytes: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Valid: true},
			AssetCode:           "ETH",
			Side:                "sell",
			OrderType:           "limit",
			Status:              "open",
			PriceJpy:            "300000.00000000",
			OrderedUnits:        "0.01000000",
			FilledUnits:         "0.00000000",
			RemainingUnits:      "0.01000000",
			FeeJpy:              "0.00000000",
			IsFeeFree:           false,
			PlacedAt:            pgTime(time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)),
			LastStatusCheckedAt: pgTime(time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC)),
		},
	}
	exchangeStub := &fakeExchangeClient{
		rules: []gmo.SymbolRule{{Symbol: "ETH_JPY", MinOrderSize: "0.01", SizeStep: "0.01"}},
	}

	service := NewService(storeStub, exchangeStub, true)
	service.now = func() time.Time { return time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC) }

	if _, err := service.CreateLimitOrder(context.Background(), CreateInput{
		AssetCode: "ETH",
		Side:      "sell",
		PriceJpy:  "300000",
		Units:     "0.01",
	}); err != nil {
		t.Fatalf("CreateLimitOrder returned error: %v", err)
	}

	if exchangeStub.createReq.Symbol != "" {
		t.Fatalf("did not expect live order creation in dry-run: %+v", exchangeStub.createReq)
	}
	if len(storeStub.insertedOrderEvents) != 1 {
		t.Fatalf("expected one order event, got %d", len(storeStub.insertedOrderEvents))
	}
}

func TestCreateLimitOrderRejectsInvalidUnits(t *testing.T) {
	t.Parallel()

	service := NewService(&fakeStore{}, &fakeExchangeClient{
		rules: []gmo.SymbolRule{{Symbol: "BTC_JPY", MinOrderSize: "0.001", SizeStep: "0.001"}},
	}, false)

	if _, err := service.CreateLimitOrder(context.Background(), CreateInput{
		AssetCode: "BTC",
		Side:      "buy",
		PriceJpy:  "10000000",
		Units:     "0.0005",
	}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCancelOrder(t *testing.T) {
	t.Parallel()

	storeStub := &fakeStore{
		getOrderRow: store.GetOrderRow{
			ID:              9,
			ExchangeOrderID: "456",
			Status:          "open",
		},
	}
	exchangeStub := &fakeExchangeClient{}

	service := NewService(storeStub, exchangeStub, false)
	service.now = func() time.Time { return time.Date(2026, 4, 8, 1, 2, 3, 0, time.UTC) }

	if err := service.CancelOrder(context.Background(), 9); err != nil {
		t.Fatalf("CancelOrder returned error: %v", err)
	}

	if exchangeStub.cancelOrderID != 456 {
		t.Fatalf("unexpected cancel order id: %d", exchangeStub.cancelOrderID)
	}
	if storeStub.cancelRequestedParams == nil || storeStub.cancelledParams == nil {
		t.Fatal("expected cancel state updates to be persisted")
	}
}

func TestCancelOrderBlocksLiveCancelInDryRun(t *testing.T) {
	t.Parallel()

	storeStub := &fakeStore{
		getOrderRow: store.GetOrderRow{
			ID:              9,
			ExchangeOrderID: "456",
			Status:          "open",
		},
	}

	service := NewService(storeStub, &fakeExchangeClient{}, true)
	if err := service.CancelOrder(context.Background(), 9); err != ErrDryRunLiveCancel {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReconcileOrders(t *testing.T) {
	t.Parallel()

	storeStub := &fakeStore{
		reconcilableOrders: []store.ListReconcilableOrdersRow{
			{
				ID:              1,
				ExchangeOrderID: "456",
				AssetCode:       "BTC",
				Status:          "open",
				OrderedUnits:    "0.00200000",
				FilledUnits:     "0.00000000",
			},
		},
	}
	exchangeStub := &fakeExchangeClient{
		ordersByBatch: []gmo.Order{
			{
				OrderID:      456,
				Symbol:       "BTC_JPY",
				Size:         "0.002",
				ExecutedSize: "0.001",
				Status:       "ORDERED",
				TimeInForce:  "SOK",
				Timestamp:    time.Date(2026, 4, 8, 0, 0, 0, 0, time.UTC),
			},
		},
		executionsByOrder: map[int64][]gmo.Execution{
			456: {
				{
					ExecutionID: 11,
					OrderID:     456,
					Symbol:      "BTC_JPY",
					Size:        "0.001",
					Price:       "10000000",
					Fee:         "0",
					Timestamp:   time.Date(2026, 4, 8, 0, 1, 0, 0, time.UTC),
				},
			},
		},
	}

	service := NewService(storeStub, exchangeStub, false)
	service.now = func() time.Time { return time.Date(2026, 4, 8, 0, 2, 0, 0, time.UTC) }

	jobRunID, err := service.ReconcileOrders(context.Background(), "tester", "manual")
	if err != nil {
		t.Fatalf("ReconcileOrders returned error: %v", err)
	}

	if jobRunID != 1 {
		t.Fatalf("unexpected job run id: %d", jobRunID)
	}
	if storeStub.markJobRunSucceededParam == nil {
		t.Fatal("expected job run to be marked succeeded")
	}
	if storeStub.updateOrderAfterSync == nil || storeStub.updateOrderAfterSync.Status != "partially_filled" {
		t.Fatalf("unexpected synced order update: %+v", storeStub.updateOrderAfterSync)
	}
	if len(storeStub.insertedTradeExecutions) != 1 {
		t.Fatalf("unexpected execution count: %d", len(storeStub.insertedTradeExecutions))
	}
}

func TestIsNotFound(t *testing.T) {
	t.Parallel()

	if !IsNotFound(pgx.ErrNoRows) {
		t.Fatal("expected pgx.ErrNoRows to be recognized")
	}
	if IsNotFound(context.DeadlineExceeded) {
		t.Fatal("did not expect unrelated error to be recognized")
	}
}
