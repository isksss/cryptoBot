package api

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/isksss/cryptoBot/internal/store"
)

const defaultListLimit = 100

type pinger interface {
	Ping(context.Context) error
}

type priceSyncer interface {
	SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error)
}

type Server struct {
	queries          store.Querier
	ping             pinger
	priceSyncer      priceSyncer
	weeklyLimitUnits string
	now              func() time.Time
}

func NewHandler(queries store.Querier, ping pinger, priceSyncer priceSyncer, weeklyLimitUnits string) *Server {
	if weeklyLimitUnits == "" {
		weeklyLimitUnits = "0"
	}

	return &Server{
		queries:          queries,
		ping:             ping,
		priceSyncer:      priceSyncer,
		weeklyLimitUnits: weeklyLimitUnits,
		now:              time.Now,
	}
}

func (h *Server) GetHealth(ctx context.Context, _ GetHealthRequestObject) (GetHealthResponseObject, error) {
	if err := h.ping.Ping(ctx); err != nil {
		return nil, err
	}

	return GetHealth200JSONResponse{
		Status:    "ok",
		CheckedAt: h.now().UTC(),
	}, nil
}

func (h *Server) GetLatestBalances(ctx context.Context, request GetLatestBalancesRequestObject) (GetLatestBalancesResponseObject, error) {
	rows, err := h.queries.ListLatestBalances(ctx)
	if err != nil {
		return nil, err
	}

	var filter *BalanceAssetCode
	if request.Params.AssetCode != nil {
		value := BalanceAssetCode(*request.Params.AssetCode)
		filter = &value
	}

	balances := make([]SummaryAsset, 0, len(rows))
	for _, row := range rows {
		assetCode := BalanceAssetCode(row.AssetCode)
		if filter != nil && assetCode != *filter {
			continue
		}
		balances = append(balances, SummaryAsset{
			AssetCode:       assetCode,
			AvailableAmount: row.AvailableAmount,
			LockedAmount:    row.LockedAmount,
		})
	}

	return GetLatestBalances200JSONResponse{
		Balances: balances,
	}, nil
}

func (h *Server) GetLatestPrices(ctx context.Context, request GetLatestPricesRequestObject) (GetLatestPricesResponseObject, error) {
	rows, err := h.queries.ListLatestPrices(ctx, optionalAssetCode(request.Params.AssetCode))
	if err != nil {
		return nil, err
	}

	prices := make([]PriceSnapshot, 0, len(rows))
	for _, row := range rows {
		prices = append(prices, PriceSnapshot{
			Id:         row.ID,
			AssetCode:  AssetCode(row.AssetCode),
			PriceJpy:   row.PriceJpy,
			CapturedAt: mustTime(row.CapturedAt),
			Source:     row.Source,
		})
	}

	return GetLatestPrices200JSONResponse{
		Prices: prices,
	}, nil
}

func (h *Server) ListPriceHistory(ctx context.Context, request ListPriceHistoryRequestObject) (ListPriceHistoryResponseObject, error) {
	rows, err := h.queries.ListPriceHistory(ctx, store.ListPriceHistoryParams{
		AssetCode:  string(request.Params.AssetCode),
		FromAt:     toPgTimestamptzOrZero(request.Params.From),
		ToAt:       toPgTimestamptzOrZero(request.Params.To),
		LimitCount: int32(limitOrDefault(request.Params.Limit)),
	})
	if err != nil {
		return nil, err
	}

	prices := make([]PriceSnapshot, 0, len(rows))
	for _, row := range rows {
		prices = append(prices, PriceSnapshot{
			Id:         row.ID,
			AssetCode:  AssetCode(row.AssetCode),
			PriceJpy:   row.PriceJpy,
			CapturedAt: mustTime(row.CapturedAt),
			Source:     row.Source,
		})
	}

	return ListPriceHistory200JSONResponse{
		Prices: prices,
	}, nil
}

func (h *Server) ListOrders(ctx context.Context, request ListOrdersRequestObject) (ListOrdersResponseObject, error) {
	rows, err := h.queries.ListOrders(ctx, store.ListOrdersParams{
		AssetCode:  optionalAssetCode(request.Params.AssetCode),
		Side:       optionalOrderSide(request.Params.Side),
		Status:     optionalOrderStatus(request.Params.Status),
		LimitCount: int32(limitOrDefault(request.Params.Limit)),
	})
	if err != nil {
		return nil, err
	}

	orders := make([]Order, 0, len(rows))
	for _, row := range rows {
		orders = append(orders, toAPIOrder(
			row.ID,
			row.ExchangeOrderID,
			row.ClientOrderID,
			row.AssetCode,
			row.Side,
			row.OrderType,
			row.Status,
			row.PriceJpy,
			row.OrderedUnits,
			row.FilledUnits,
			row.RemainingUnits,
			row.FeeJpy,
			row.IsFeeFree,
			row.PlacedAt,
			row.ExpiresAt,
			row.CancelledAt,
			row.LastStatusCheckedAt,
		))
	}

	return ListOrders200JSONResponse{
		Orders: orders,
	}, nil
}

func (h *Server) GetOrder(ctx context.Context, request GetOrderRequestObject) (GetOrderResponseObject, error) {
	row, err := h.queries.GetOrder(ctx, request.OrderId)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GetOrder404JSONResponse{
				NotFoundJSONResponse: NotFoundJSONResponse{
					Code:    "not_found",
					Message: "order not found",
				},
			}, nil
		}
		return nil, err
	}

	eventsRows, err := h.queries.ListOrderEventsByOrderID(ctx, request.OrderId)
	if err != nil {
		return nil, err
	}

	events := make([]OrderEvent, 0, len(eventsRows))
	for _, event := range eventsRows {
		events = append(events, OrderEvent{
			Id:         event.ID,
			EventType:  event.EventType,
			FromStatus: toOrderStatusPtr(event.FromStatus),
			ToStatus:   toOrderStatusPtr(event.ToStatus),
			EventAt:    mustTime(event.EventAt),
			Payload:    jsonBytesToMapPtr(event.Payload),
		})
	}

	return GetOrder200JSONResponse{
		Order: toAPIOrder(
			row.ID,
			row.ExchangeOrderID,
			row.ClientOrderID,
			row.AssetCode,
			row.Side,
			row.OrderType,
			row.Status,
			row.PriceJpy,
			row.OrderedUnits,
			row.FilledUnits,
			row.RemainingUnits,
			row.FeeJpy,
			row.IsFeeFree,
			row.PlacedAt,
			row.ExpiresAt,
			row.CancelledAt,
			row.LastStatusCheckedAt,
		),
		Events: events,
	}, nil
}

func (h *Server) ListExecutions(ctx context.Context, request ListExecutionsRequestObject) (ListExecutionsResponseObject, error) {
	rows, err := h.queries.ListExecutions(ctx, store.ListExecutionsParams{
		AssetCode:  optionalAssetCode(request.Params.AssetCode),
		OrderID:    optionalInt64(request.Params.OrderId),
		LimitCount: int32(limitOrDefault(request.Params.Limit)),
	})
	if err != nil {
		return nil, err
	}

	executions := make([]TradeExecution, 0, len(rows))
	for _, row := range rows {
		executions = append(executions, TradeExecution{
			Id:                  row.ID,
			OrderId:             row.OrderID,
			ExchangeExecutionId: row.ExchangeExecutionID,
			ExecutedAt:          mustTime(row.ExecutedAt),
			PriceJpy:            row.PriceJpy,
			ExecutedUnits:       row.ExecutedUnits,
			FeeJpy:              row.FeeJpy,
			IsPartialFill:       row.IsPartialFill,
		})
	}

	return ListExecutions200JSONResponse{
		Executions: executions,
	}, nil
}

func (h *Server) ListJobRuns(ctx context.Context, request ListJobRunsRequestObject) (ListJobRunsResponseObject, error) {
	rows, err := h.queries.ListJobRuns(ctx, store.ListJobRunsParams{
		JobType:    optionalJobType(request.Params.JobType),
		Status:     optionalJobStatus(request.Params.Status),
		LimitCount: int32(limitOrDefault(request.Params.Limit)),
	})
	if err != nil {
		return nil, err
	}

	jobRuns := make([]JobRunSummary, 0, len(rows))
	for _, row := range rows {
		jobRuns = append(jobRuns, toJobRunSummary(
			row.ID,
			row.JobType,
			row.Status,
			row.ScheduledFor,
			row.StartedAt,
			row.FinishedAt,
			row.ErrorCode,
			row.ErrorMessage,
		))
	}

	return ListJobRuns200JSONResponse{
		JobRuns: jobRuns,
	}, nil
}

func (h *Server) GetSystemSummary(ctx context.Context, _ GetSystemSummaryRequestObject) (GetSystemSummaryResponseObject, error) {
	balanceRows, err := h.queries.ListLatestBalances(ctx)
	if err != nil {
		return nil, err
	}

	jobRows, err := h.queries.ListLatestJobRuns(ctx, 5)
	if err != nil {
		return nil, err
	}

	openCount, err := h.queries.CountOpenOrders(ctx)
	if err != nil {
		return nil, err
	}

	unresolvedCount, err := h.queries.CountUnresolvedPreviousDayOrders(ctx)
	if err != nil {
		return nil, err
	}

	windowStartedAt := h.now().UTC().Add(-7 * 24 * time.Hour)
	consumedRows, err := h.queries.ListWeeklyConsumedBuyUnits(ctx, toPgTimestamptz(windowStartedAt))
	if err != nil {
		return nil, err
	}

	balances := make([]SummaryAsset, 0, len(balanceRows))
	for _, row := range balanceRows {
		balances = append(balances, SummaryAsset{
			AssetCode:       BalanceAssetCode(row.AssetCode),
			AvailableAmount: row.AvailableAmount,
			LockedAmount:    row.LockedAmount,
		})
	}

	jobRuns := make([]JobRunSummary, 0, len(jobRows))
	for _, row := range jobRows {
		jobRuns = append(jobRuns, toJobRunSummary(
			row.ID,
			row.JobType,
			row.Status,
			row.ScheduledFor,
			row.StartedAt,
			row.FinishedAt,
			row.ErrorCode,
			row.ErrorMessage,
		))
	}

	consumedByAsset := map[string]string{}
	for _, row := range consumedRows {
		consumedByAsset[row.AssetCode] = row.ConsumedUnits
	}

	weeklyLimits := []SummaryWeeklyLimit{
		{
			AssetCode:       AssetCodeBTC,
			LimitUnits:      h.weeklyLimitUnits,
			ConsumedUnits:   consumedOrZero(consumedByAsset, "BTC"),
			RemainingUnits:  subtractDecimalStrings(h.weeklyLimitUnits, consumedOrZero(consumedByAsset, "BTC")),
			WindowStartedAt: windowStartedAt,
		},
		{
			AssetCode:       AssetCodeETH,
			LimitUnits:      h.weeklyLimitUnits,
			ConsumedUnits:   consumedOrZero(consumedByAsset, "ETH"),
			RemainingUnits:  subtractDecimalStrings(h.weeklyLimitUnits, consumedOrZero(consumedByAsset, "ETH")),
			WindowStartedAt: windowStartedAt,
		},
	}

	return GetSystemSummary200JSONResponse{
		ServerTime: h.now().UTC(),
		LatestJobRuns: jobRuns,
		Balances: balances,
		OrderStats: SummaryOrderStats{
			OpenCount:                  int32(openCount),
			UnresolvedPreviousDayCount: int32(unresolvedCount),
		},
		WeeklyLimits: weeklyLimits,
	}, nil
}

func (h *Server) TriggerPriceFetchJob(ctx context.Context, request TriggerPriceFetchJobRequestObject) (TriggerPriceFetchJobResponseObject, error) {
	if h.priceSyncer == nil {
		return nil, errors.New("price sync service is not configured")
	}

	jobRunID, err := h.priceSyncer.SyncPriceAndBalances(ctx, requestedBy(request.Body), requestedReason(request.Body))
	if err != nil {
		return nil, err
	}

	return TriggerPriceFetchJob202JSONResponse{
		JobRunId: jobRunID,
		Status:   "accepted",
	}, nil
}

func (h *Server) TriggerOrderReconcileJob(ctx context.Context, request TriggerOrderReconcileJobRequestObject) (TriggerOrderReconcileJobResponseObject, error) {
	jobRun, err := h.insertManualJobRun(ctx, OrderReconcile, request.Body)
	if err != nil {
		return nil, err
	}

	return TriggerOrderReconcileJob202JSONResponse{
		JobRunId: jobRun.ID,
		Status:   "accepted",
	}, nil
}

func (h *Server) TriggerDailyTradeJob(ctx context.Context, request TriggerDailyTradeJobRequestObject) (TriggerDailyTradeJobResponseObject, error) {
	jobRun, err := h.insertManualJobRun(ctx, DailyTrade, request.Body)
	if err != nil {
		return nil, err
	}

	return TriggerDailyTradeJob202JSONResponse{
		JobRunId: jobRun.ID,
		Status:   "accepted",
	}, nil
}

func (h *Server) insertManualJobRun(ctx context.Context, jobType JobType, body *TriggerJobRequest) (store.InsertJobRunRow, error) {
	now := h.now().UTC()
	metadata, err := json.Marshal(body)
	if err != nil {
		return store.InsertJobRunRow{}, err
	}

	return h.queries.InsertJobRun(ctx, store.InsertJobRunParams{
		JobType:      string(jobType),
		Status:       string(JobRunStatusRunning),
		ScheduledFor: toPgTimestamptz(now),
		StartedAt:    toPgTimestamptz(now),
		Metadata:     metadata,
	})
}

func toAPIOrder(
	id int64,
	exchangeOrderID string,
	clientOrderID pgtype.UUID,
	assetCode string,
	side string,
	orderType string,
	status string,
	priceJpy string,
	orderedUnits string,
	filledUnits string,
	remainingUnits string,
	feeJpy string,
	isFeeFree bool,
	placedAt pgtype.Timestamptz,
	expiresAt pgtype.Timestamptz,
	cancelledAt pgtype.Timestamptz,
	lastStatusCheckedAt pgtype.Timestamptz,
) Order {
	return Order{
		Id:                  id,
		ExchangeOrderId:     exchangeOrderID,
		ClientOrderId:       toUUID(clientOrderID),
		AssetCode:           AssetCode(assetCode),
		Side:                OrderSide(side),
		OrderType:           OrderType(orderType),
		Status:              OrderStatus(status),
		PriceJpy:            priceJpy,
		OrderedUnits:        orderedUnits,
		FilledUnits:         filledUnits,
		RemainingUnits:      remainingUnits,
		FeeJpy:              feeJpy,
		IsFeeFree:           isFeeFree,
		PlacedAt:            mustTime(placedAt),
		ExpiresAt:           timePtr(expiresAt),
		CancelledAt:         timePtr(cancelledAt),
		LastStatusCheckedAt: timePtr(lastStatusCheckedAt),
	}
}

func toJobRunSummary(
	id int64,
	jobType string,
	status string,
	scheduledFor pgtype.Timestamptz,
	startedAt pgtype.Timestamptz,
	finishedAt pgtype.Timestamptz,
	errorCode *string,
	errorMessage *string,
) JobRunSummary {
	return JobRunSummary{
		Id:           id,
		JobType:      JobType(jobType),
		Status:       JobRunStatus(status),
		ScheduledFor: mustTime(scheduledFor),
		StartedAt:    mustTime(startedAt),
		FinishedAt:   timePtr(finishedAt),
		ErrorCode:    errorCode,
		ErrorMessage: errorMessage,
	}
}

func optionalAssetCode(code *AssetCode) *string {
	if code == nil {
		return nil
	}
	value := string(*code)
	return &value
}

func optionalOrderSide(side *OrderSide) *string {
	if side == nil {
		return nil
	}
	value := string(*side)
	return &value
}

func optionalOrderStatus(status *OrderStatus) *string {
	if status == nil {
		return nil
	}
	value := string(*status)
	return &value
}

func optionalJobType(jobType *JobType) *string {
	if jobType == nil {
		return nil
	}
	value := string(*jobType)
	return &value
}

func optionalJobStatus(status *JobStatus) *string {
	if status == nil {
		return nil
	}
	value := string(*status)
	return &value
}

func optionalInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func limitOrDefault(limit *int) int {
	if limit == nil || *limit <= 0 {
		return defaultListLimit
	}
	return *limit
}

func toPgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  t,
		Valid: true,
	}
}

func toPgTimestamptzOrZero(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return toPgTimestamptz(*t)
}

func mustTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}

func timePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	value := ts.Time
	return &value
}

func toUUID(id pgtype.UUID) openapi_types.UUID {
	var out openapi_types.UUID
	if !id.Valid {
		return out
	}
	copy(out[:], id.Bytes[:])
	return out
}

func toOrderStatusPtr(status *string) *OrderStatus {
	if status == nil {
		return nil
	}
	value := OrderStatus(*status)
	return &value
}

func jsonBytesToMapPtr(raw []byte) *map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return &payload
}

func consumedOrZero(values map[string]string, asset string) string {
	if value, ok := values[asset]; ok {
		return value
	}
	return "0"
}

func subtractDecimalStrings(limit string, consumed string) string {
	limitRat, ok := new(big.Rat).SetString(limit)
	if !ok {
		return "0"
	}

	consumedRat, ok := new(big.Rat).SetString(consumed)
	if !ok {
		return "0"
	}

	result := new(big.Rat).Sub(limitRat, consumedRat)
	if result.Sign() < 0 {
		return "0"
	}

	return result.FloatString(8)
}

func requestedBy(body *TriggerJobRequest) string {
	if body == nil || body.RequestedBy == nil || *body.RequestedBy == "" {
		return "api"
	}
	return *body.RequestedBy
}

func requestedReason(body *TriggerJobRequest) string {
	if body == nil || body.Reason == nil {
		return ""
	}
	return *body.Reason
}
