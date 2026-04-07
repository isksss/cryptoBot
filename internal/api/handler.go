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

	"github.com/isksss/cryptoBot/internal/order"
	"github.com/isksss/cryptoBot/internal/store"
)

const defaultListLimit = 100

// pinger は /healthz で使う DB 疎通確認の抽象です。
type pinger interface {
	Ping(context.Context) error
}

// priceSyncer は価格・残高同期ジョブの抽象です。
type priceSyncer interface {
	SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error)
}

// orderService は注文作成・取消・状態同期を担う抽象です。
type orderService interface {
	CreateLimitOrder(ctx context.Context, input order.CreateInput) (store.InsertOrderRow, error)
	CancelOrder(ctx context.Context, localOrderID int64) error
	ReconcileOrders(ctx context.Context, requestedBy string, reason string) (int64, error)
}

// Server は OpenAPI 生成 interface の手書き実装です。
type Server struct {
	queries          store.Querier
	ping             pinger
	priceSyncer      priceSyncer
	orderService     orderService
	weeklyLimitUnits string
	now              func() time.Time
}

// NewHandler は管理 API に必要な依存関係を束ねます。
func NewHandler(queries store.Querier, ping pinger, priceSyncer priceSyncer, orderService orderService, weeklyLimitUnits string) *Server {
	if weeklyLimitUnits == "" {
		weeklyLimitUnits = "0"
	}

	return &Server{
		queries:          queries,
		ping:             ping,
		priceSyncer:      priceSyncer,
		orderService:     orderService,
		weeklyLimitUnits: weeklyLimitUnits,
		now:              time.Now,
	}
}

// GetHealth は DB 到達性を含む簡易ヘルスチェックです。
func (h *Server) GetHealth(ctx context.Context, _ GetHealthRequestObject) (GetHealthResponseObject, error) {
	if err := h.ping.Ping(ctx); err != nil {
		return nil, err
	}

	return GetHealth200JSONResponse{
		Status:    "ok",
		CheckedAt: h.now().UTC(),
	}, nil
}

// GetLatestBalances は各資産の最新残高スナップショットを返します。
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

	return GetLatestBalances200JSONResponse{Balances: balances}, nil
}

// CreateOrder は新規指値注文を作成し、ローカル注文として保存します。
func (h *Server) CreateOrder(ctx context.Context, request CreateOrderRequestObject) (CreateOrderResponseObject, error) {
	if h.orderService == nil {
		return nil, errors.New("order service is not configured")
	}
	if request.Body == nil {
		return nil, errors.New("request body is required")
	}

	row, err := h.orderService.CreateLimitOrder(ctx, order.CreateInput{
		AssetCode:   string(request.Body.AssetCode),
		Side:        string(request.Body.Side),
		PriceJpy:    request.Body.PriceJpy,
		Units:       request.Body.Units,
		TimeInForce: stringValue(request.Body.TimeInForce),
		RequestedBy: stringValue(request.Body.RequestedBy),
	})
	if err != nil {
		return nil, err
	}

	return CreateOrder201JSONResponse{
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
	}, nil
}

// GetLatestPrices は最新価格を返し、必要なら資産で絞り込みます。
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

	return GetLatestPrices200JSONResponse{Prices: prices}, nil
}

// ListPriceHistory は指定資産の価格履歴を返します。
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

	return ListPriceHistory200JSONResponse{Prices: prices}, nil
}

// ListOrders は保存済み注文を条件付きで列挙します。
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

	return ListOrders200JSONResponse{Orders: orders}, nil
}

// GetOrder は単一注文とその状態遷移履歴を返します。
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

// CancelOrder は注文取消を実行し、ローカル状態も更新します。
func (h *Server) CancelOrder(ctx context.Context, request CancelOrderRequestObject) (CancelOrderResponseObject, error) {
	if h.orderService == nil {
		return nil, errors.New("order service is not configured")
	}

	if err := h.orderService.CancelOrder(ctx, request.OrderId); err != nil {
		if order.IsNotFound(err) {
			return CancelOrder404JSONResponse{
				NotFoundJSONResponse: NotFoundJSONResponse{
					Code:    "not_found",
					Message: "order not found",
				},
			}, nil
		}
		return nil, err
	}

	return CancelOrder200JSONResponse{
		OrderId: request.OrderId,
		Status:  OrderStatusCancelled,
	}, nil
}

// ListExecutions は保存済み約定履歴を返します。
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

	return ListExecutions200JSONResponse{Executions: executions}, nil
}

// ListJobRuns は直近のジョブ実行履歴を返します。
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

	return ListJobRuns200JSONResponse{JobRuns: jobRuns}, nil
}

// GetSystemSummary はダッシュボード向けの集計情報を返します。
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
		ServerTime:    h.now().UTC(),
		LatestJobRuns: jobRuns,
		Balances:      balances,
		OrderStats: SummaryOrderStats{
			OpenCount:                  int32(openCount),
			UnresolvedPreviousDayCount: int32(unresolvedCount),
		},
		WeeklyLimits: weeklyLimits,
	}, nil
}

// TriggerPriceFetchJob は価格・残高同期ジョブを即時実行します。
func (h *Server) TriggerPriceFetchJob(ctx context.Context, request TriggerPriceFetchJobRequestObject) (TriggerPriceFetchJobResponseObject, error) {
	if h.priceSyncer == nil {
		return nil, errors.New("price sync service is not configured")
	}

	jobRunID, err := h.priceSyncer.SyncPriceAndBalances(ctx, requestedBy(request.Body), requestedReason(request.Body))
	if err != nil {
		return nil, err
	}

	return TriggerPriceFetchJob202JSONResponse{JobRunId: jobRunID, Status: "accepted"}, nil
}

// TriggerOrderReconcileJob は注文状態同期ジョブを即時実行します。
func (h *Server) TriggerOrderReconcileJob(ctx context.Context, request TriggerOrderReconcileJobRequestObject) (TriggerOrderReconcileJobResponseObject, error) {
	if h.orderService == nil {
		return nil, errors.New("order service is not configured")
	}

	jobRunID, err := h.orderService.ReconcileOrders(ctx, requestedBy(request.Body), requestedReason(request.Body))
	if err != nil {
		return nil, err
	}

	return TriggerOrderReconcileJob202JSONResponse{JobRunId: jobRunID, Status: "accepted"}, nil
}

// TriggerDailyTradeJob は日次売買ジョブの手動起票だけを行います。
func (h *Server) TriggerDailyTradeJob(ctx context.Context, request TriggerDailyTradeJobRequestObject) (TriggerDailyTradeJobResponseObject, error) {
	jobRun, err := h.insertManualJobRun(ctx, DailyTrade, request.Body)
	if err != nil {
		return nil, err
	}

	return TriggerDailyTradeJob202JSONResponse{JobRunId: jobRun.ID, Status: "accepted"}, nil
}

// insertManualJobRun は job_runs に手動起票を記録します。
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

// toAPIOrder は sqlc の注文行を API モデルへ変換します。
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

// toJobRunSummary は sqlc のジョブ行を API 要約モデルへ変換します。
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

// optionalAssetCode は enum フィルタを sqlc 向けの nullable param へ変換します。
func optionalAssetCode(code *AssetCode) *string {
	if code == nil {
		return nil
	}
	value := string(*code)
	return &value
}

// optionalOrderSide は enum フィルタを sqlc 向けの nullable param へ変換します。
func optionalOrderSide(side *OrderSide) *string {
	if side == nil {
		return nil
	}
	value := string(*side)
	return &value
}

// optionalOrderStatus は enum フィルタを sqlc 向けの nullable param へ変換します。
func optionalOrderStatus(status *OrderStatus) *string {
	if status == nil {
		return nil
	}
	value := string(*status)
	return &value
}

// optionalJobType は enum フィルタを sqlc 向けの nullable param へ変換します。
func optionalJobType(jobType *JobType) *string {
	if jobType == nil {
		return nil
	}
	value := string(*jobType)
	return &value
}

// optionalJobStatus は enum フィルタを sqlc 向けの nullable param へ変換します。
func optionalJobStatus(status *JobStatus) *string {
	if status == nil {
		return nil
	}
	value := string(*status)
	return &value
}

// optionalInt64 は nullable bigint パラメータ用のコピーを返します。
func optionalInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// limitOrDefault は未指定時に既定の件数を適用します。
func limitOrDefault(limit *int) int {
	if limit == nil || *limit <= 0 {
		return defaultListLimit
	}
	return *limit
}

// toPgTimestamptz は sqlc 用の timestamptz を作ります。
func toPgTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// toPgTimestamptzOrZero は optional time を nullable sqlc param に変換します。
func toPgTimestamptzOrZero(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return toPgTimestamptz(*t)
}

// mustTime は sqlc 行の pgtype 時刻を time.Time へ展開します。
func mustTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}

// timePtr は nullable 時刻を API の pointer へ変換します。
func timePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	value := ts.Time
	return &value
}

// toUUID は pgtype.UUID を OpenAPI の UUID 型へ変換します。
func toUUID(id pgtype.UUID) openapi_types.UUID {
	var out openapi_types.UUID
	if !id.Valid {
		return out
	}
	copy(out[:], id.Bytes[:])
	return out
}

// toOrderStatusPtr は nullable status を API enum pointer へ変換します。
func toOrderStatusPtr(status *string) *OrderStatus {
	if status == nil {
		return nil
	}
	value := OrderStatus(*status)
	return &value
}

// jsonBytesToMapPtr は JSONB payload を汎用 map として返します。
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

// consumedOrZero は使用量が存在しない資産を 0 扱いにします。
func consumedOrZero(values map[string]string, asset string) string {
	if value, ok := values[asset]; ok {
		return value
	}
	return "0"
}

// subtractDecimalStrings は週次上限表示用の小数減算を行います。
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

// requestedBy は手動起票リクエストから実行主体を取り出します。
func requestedBy(body *TriggerJobRequest) string {
	if body == nil || body.RequestedBy == nil || *body.RequestedBy == "" {
		return "api"
	}
	return *body.RequestedBy
}

// requestedReason は手動起票リクエストから理由を取り出します。
func requestedReason(body *TriggerJobRequest) string {
	if body == nil || body.Reason == nil {
		return ""
	}
	return *body.Reason
}

// stringValue は生成コードの optional alias を素の string に戻します。
func stringValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
