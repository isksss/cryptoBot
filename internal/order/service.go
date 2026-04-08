package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/gmo"
	"github.com/isksss/cryptoBot/internal/store"
)

const (
	reconcileBatchSize          = 10
	dryRunExchangeOrderIDPrefix = "dryrun-"
	defaultTimeInForce          = "SOK"
)

var (
	// ErrDryRunLiveCancel は dry-run 中に live 注文の取消を防ぐためのエラーです。
	ErrDryRunLiveCancel = errors.New("dry-run mode prevents cancelling live exchange orders")
	// ErrJobSkipped はジョブを安全上の理由でスキップしたことを表す内部エラーです。
	ErrJobSkipped = errors.New("job skipped")
	// ErrInvalidOrderInput は注文入力の形式が不正なことを表します。
	ErrInvalidOrderInput = errors.New("invalid order input")
)

// Store は注文作成・同期・日次売買に必要な永続化操作を表します。
type Store interface {
	InsertJobRun(ctx context.Context, arg store.InsertJobRunParams) (store.InsertJobRunRow, error)
	MarkJobRunFailed(ctx context.Context, arg store.MarkJobRunFailedParams) error
	MarkJobRunSucceeded(ctx context.Context, arg store.MarkJobRunSucceededParams) error
	MarkJobRunSkipped(ctx context.Context, arg store.MarkJobRunSkippedParams) error
	InsertOrder(ctx context.Context, arg store.InsertOrderParams) (store.InsertOrderRow, error)
	InsertOrderEvent(ctx context.Context, arg store.InsertOrderEventParams) error
	GetOrder(ctx context.Context, id int64) (store.GetOrderRow, error)
	ListLatestBalances(ctx context.Context) ([]store.ListLatestBalancesRow, error)
	ListLatestPrices(ctx context.Context, assetCode *string) ([]store.ListLatestPricesRow, error)
	ListWeeklyConsumedBuyUnits(ctx context.Context, windowStartedAt pgtype.Timestamptz) ([]store.ListWeeklyConsumedBuyUnitsRow, error)
	CountOpenOrders(ctx context.Context) (int64, error)
	CountUnresolvedPreviousDayOrders(ctx context.Context) (int64, error)
	CountJobRunsByTypeInWindow(ctx context.Context, arg store.CountJobRunsByTypeInWindowParams) (int64, error)
	ListReconcilableOrders(ctx context.Context, limitCount int32) ([]store.ListReconcilableOrdersRow, error)
	UpdateOrderAfterSync(ctx context.Context, arg store.UpdateOrderAfterSyncParams) error
	InsertTradeExecution(ctx context.Context, arg store.InsertTradeExecutionParams) error
	MarkOrderCancelRequested(ctx context.Context, arg store.MarkOrderCancelRequestedParams) error
	MarkOrderCancelled(ctx context.Context, arg store.MarkOrderCancelledParams) error
}

// ExchangeClient は GMO の注文関連 API に対する抽象です。
type ExchangeClient interface {
	GetSymbolRules(ctx context.Context) ([]gmo.SymbolRule, error)
	GetOrders(ctx context.Context, orderIDs []int64) ([]gmo.Order, error)
	GetExecutions(ctx context.Context, orderID int64) ([]gmo.Execution, error)
	CreateOrder(ctx context.Context, reqBody gmo.CreateOrderRequest) (gmo.CreateOrderResponse, error)
	CancelOrder(ctx context.Context, orderID int64) error
}

// CreateInput は API や戦略から受け取る新規注文要求の内部表現です。
type CreateInput struct {
	AssetCode   string
	Side        string
	PriceJpy    string
	Units       string
	TimeInForce string
	RequestedBy string
}

// Service は GMO と PostgreSQL の間で注文ライフサイクルを仲介します。
type Service struct {
	store            Store
	client           ExchangeClient
	dryRun           bool
	weeklyLimitUnits string
	now              func() time.Time
}

// NewService は注文サービスを初期化します。
func NewService(store Store, client ExchangeClient, dryRun bool, weeklyLimitUnits string) *Service {
	if weeklyLimitUnits == "" {
		weeklyLimitUnits = "0"
	}

	return &Service{
		store:            store,
		client:           client,
		dryRun:           dryRun,
		weeklyLimitUnits: weeklyLimitUnits,
		now:              time.Now,
	}
}

// CreateLimitOrder は最小数量と刻みを検証し、必要なら GMO に発注してローカル注文を保存します。
func (s *Service) CreateLimitOrder(ctx context.Context, input CreateInput) (store.InsertOrderRow, error) {
	assetCode := strings.ToUpper(input.AssetCode)
	side := strings.ToLower(input.Side)
	timeInForce := input.TimeInForce
	if timeInForce == "" {
		timeInForce = defaultTimeInForce
	}
	requestedBy := strings.TrimSpace(input.RequestedBy)
	if requestedBy == "" {
		requestedBy = "system"
	}

	if err := validateCreateInput(assetCode, side, input.PriceJpy, input.Units, timeInForce); err != nil {
		return store.InsertOrderRow{}, err
	}

	rule, err := s.getRule(ctx, assetCode)
	if err != nil {
		return store.InsertOrderRow{}, err
	}
	if err := validateOrderUnits(input.Units, rule.MinOrderSize, rule.SizeStep); err != nil {
		return store.InsertOrderRow{}, err
	}

	now := s.now().UTC()
	clientOrderID := uuid.New()
	exchangeOrderID := dryRunExchangeOrderIDPrefix + clientOrderID.String()
	if !s.dryRun {
		resp, err := s.client.CreateOrder(ctx, gmo.CreateOrderRequest{
			Symbol:        symbolToPair(assetCode),
			Side:          strings.ToUpper(side),
			ExecutionType: "LIMIT",
			TimeInForce:   timeInForce,
			Price:         input.PriceJpy,
			Size:          input.Units,
		})
		if err != nil {
			return store.InsertOrderRow{}, err
		}
		exchangeOrderID = resp.OrderID
	}

	row, err := s.store.InsertOrder(ctx, store.InsertOrderParams{
		ExchangeOrderID:     exchangeOrderID,
		ClientOrderID:       pgUUID(clientOrderID),
		AssetCode:           assetCode,
		Side:                side,
		OrderType:           "limit",
		Status:              "open",
		PriceJpy:            mustNumeric(input.PriceJpy),
		OrderedUnits:        mustNumeric(input.Units),
		FilledUnits:         mustNumeric("0"),
		RemainingUnits:      mustNumeric(input.Units),
		FeeJpy:              mustNumeric("0"),
		IsFeeFree:           timeInForce == "SOK",
		PlacedAt:            pgTime(now),
		LastStatusCheckedAt: pgTime(now),
	})
	if err != nil {
		return store.InsertOrderRow{}, err
	}

	payload, _ := json.Marshal(map[string]any{
		"requestedBy": requestedBy,
		"timeInForce": timeInForce,
		"dryRun":      s.dryRun,
		"rule": map[string]string{
			"symbol":       rule.Symbol,
			"minOrderSize": rule.MinOrderSize,
			"sizeStep":     rule.SizeStep,
			"tickSize":     rule.TickSize,
		},
	})
	if err := s.store.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		OrderID:   row.ID,
		EventType: "opened",
		ToStatus:  stringPtr("open"),
		EventAt:   pgTime(now),
		Payload:   payload,
	}); err != nil {
		return store.InsertOrderRow{}, err
	}

	return row, nil
}

// CancelOrder は live 注文か dry-run 注文かを判定して安全側に取消します。
func (s *Service) CancelOrder(ctx context.Context, localOrderID int64) error {
	row, err := s.store.GetOrder(ctx, localOrderID)
	if err != nil {
		return err
	}

	now := pgTime(s.now().UTC())
	if err := s.store.MarkOrderCancelRequested(ctx, store.MarkOrderCancelRequestedParams{
		ID:        localOrderID,
		CheckedAt: now,
	}); err != nil {
		return err
	}
	if err := s.store.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		OrderID:    localOrderID,
		EventType:  "cancel_requested",
		FromStatus: stringPtr(row.Status),
		ToStatus:   stringPtr("cancel_requested"),
		EventAt:    now,
		Payload:    []byte(`{}`),
	}); err != nil {
		return err
	}

	switch {
	case isDryRunExchangeOrderID(row.ExchangeOrderID):
	case s.dryRun:
		return ErrDryRunLiveCancel
	default:
		exchangeOrderID, err := strconv.ParseInt(row.ExchangeOrderID, 10, 64)
		if err != nil {
			return fmt.Errorf("exchange order id の解析に失敗: %w", err)
		}
		if err := s.client.CancelOrder(ctx, exchangeOrderID); err != nil {
			return err
		}
	}

	if err := s.store.MarkOrderCancelled(ctx, store.MarkOrderCancelledParams{
		ID:          localOrderID,
		CancelledAt: now,
	}); err != nil {
		return err
	}

	return s.store.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		OrderID:    localOrderID,
		EventType:  "cancelled",
		FromStatus: stringPtr("cancel_requested"),
		ToStatus:   stringPtr("cancelled"),
		EventAt:    now,
		Payload:    []byte(`{}`),
	})
}

// DailyTrade は JST 日次売買ジョブを実行し、戦略に沿って新規注文を発行します。
func (s *Service) DailyTrade(ctx context.Context, requestedBy string, reason string) (int64, error) {
	now := s.now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"requestedBy": requestedBy,
		"reason":      reason,
		"dryRun":      s.dryRun,
	})
	if err != nil {
		return 0, err
	}

	jobRun, err := s.store.InsertJobRun(ctx, store.InsertJobRunParams{
		JobType:      "daily_trade",
		Status:       "running",
		ScheduledFor: pgTime(now),
		StartedAt:    pgTime(now),
		Metadata:     metadata,
	})
	if err != nil {
		return 0, err
	}

	err = s.dailyTrade(ctx, jobRun.ID)
	switch {
	case err == nil:
		if err := s.store.MarkJobRunSucceeded(ctx, store.MarkJobRunSucceededParams{
			ID:         jobRun.ID,
			FinishedAt: pgTime(s.now().UTC()),
		}); err != nil {
			return jobRun.ID, err
		}
		return jobRun.ID, nil
	case errors.Is(err, ErrJobSkipped):
		if err := s.store.MarkJobRunSkipped(ctx, store.MarkJobRunSkippedParams{
			ID:           jobRun.ID,
			FinishedAt:   pgTime(s.now().UTC()),
			ErrorCode:    stringPtr("daily_trade_skipped"),
			ErrorMessage: stringPtr(err.Error()),
		}); err != nil {
			return jobRun.ID, err
		}
		return jobRun.ID, nil
	default:
		if err := s.store.MarkJobRunFailed(ctx, store.MarkJobRunFailedParams{
			ID:           jobRun.ID,
			FinishedAt:   pgTime(s.now().UTC()),
			ErrorCode:    stringPtr("daily_trade_failed"),
			ErrorMessage: stringPtr(err.Error()),
		}); err != nil {
			return jobRun.ID, err
		}
		return jobRun.ID, err
	}
}

// ReconcileOrders は GMO の注文状態と約定情報を DB に反映し、job_runs に結果を残します。
func (s *Service) ReconcileOrders(ctx context.Context, requestedBy string, reason string) (int64, error) {
	now := s.now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"requestedBy": requestedBy,
		"reason":      reason,
		"dryRun":      s.dryRun,
	})
	if err != nil {
		return 0, err
	}

	jobRun, err := s.store.InsertJobRun(ctx, store.InsertJobRunParams{
		JobType:      "order_reconcile",
		Status:       "running",
		ScheduledFor: pgTime(now),
		StartedAt:    pgTime(now),
		Metadata:     metadata,
	})
	if err != nil {
		return 0, err
	}

	if err := s.reconcileOrders(ctx, jobRun.ID); err != nil {
		if err := s.store.MarkJobRunFailed(ctx, store.MarkJobRunFailedParams{
			ID:           jobRun.ID,
			FinishedAt:   pgTime(s.now().UTC()),
			ErrorCode:    stringPtr("order_reconcile_failed"),
			ErrorMessage: stringPtr(err.Error()),
		}); err != nil {
			return jobRun.ID, err
		}
		return jobRun.ID, err
	}

	if err := s.store.MarkJobRunSucceeded(ctx, store.MarkJobRunSucceededParams{
		ID:         jobRun.ID,
		FinishedAt: pgTime(s.now().UTC()),
	}); err != nil {
		return jobRun.ID, err
	}

	return jobRun.ID, nil
}

// dailyTrade は README に記載した初期売買戦略を実際の注文へ落とし込みます。
func (s *Service) dailyTrade(ctx context.Context, jobRunID int64) error {
	_ = jobRunID

	if err := s.ensureNoDuplicateDailyTrade(ctx); err != nil {
		return err
	}

	unresolvedPreviousDay, err := s.store.CountUnresolvedPreviousDayOrders(ctx)
	if err != nil {
		return err
	}
	if unresolvedPreviousDay > 0 {
		return fmt.Errorf("%w: 前日未解消注文が %d 件あります", ErrJobSkipped, unresolvedPreviousDay)
	}

	openOrders, err := s.store.CountOpenOrders(ctx)
	if err != nil {
		return err
	}
	if openOrders > 0 {
		return fmt.Errorf("%w: 未解決注文が %d 件あります", ErrJobSkipped, openOrders)
	}

	balances, prices, err := s.loadDailyTradeInputs(ctx)
	if err != nil {
		return err
	}

	rules, err := s.loadRules(ctx)
	if err != nil {
		return err
	}

	weeklyRemaining, err := s.loadWeeklyRemaining(ctx)
	if err != nil {
		return err
	}

	created := 0
	for _, asset := range []string{"BTC", "ETH"} {
		sellInput, ok, err := buildSellOrderInput(asset, balances[asset], prices[asset], rules[asset])
		if err != nil {
			return err
		}
		if ok {
			if _, err := s.CreateLimitOrder(ctx, sellInput); err != nil {
				return err
			}
			created++
		}

		buyInput, ok, err := buildBuyOrderInput(asset, balances["JPY"], prices[asset], rules[asset], weeklyRemaining[asset])
		if err != nil {
			return err
		}
		if ok {
			if _, err := s.CreateLimitOrder(ctx, buyInput); err != nil {
				return err
			}
			created++
		}
	}

	if created == 0 {
		return fmt.Errorf("%w: 発注条件を満たす注文がありません", ErrJobSkipped)
	}
	return nil
}

// reconcileOrders は未解決注文を GMO から取り直してローカル状態を前進させます。
func (s *Service) reconcileOrders(ctx context.Context, jobRunID int64) error {
	rows, err := s.store.ListReconcilableOrders(ctx, 100)
	if err != nil {
		return err
	}

	orderRowsByExchangeID := map[int64]store.ListReconcilableOrdersRow{}
	orderIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if isDryRunExchangeOrderID(row.ExchangeOrderID) {
			continue
		}
		orderID, err := strconv.ParseInt(row.ExchangeOrderID, 10, 64)
		if err != nil {
			return fmt.Errorf("exchange order id の解析に失敗: order_id=%d: %w", row.ID, err)
		}
		orderRowsByExchangeID[orderID] = row
		orderIDs = append(orderIDs, orderID)
	}
	if len(orderIDs) == 0 {
		return nil
	}

	for _, batch := range chunkInt64s(orderIDs, reconcileBatchSize) {
		exchangeOrders, err := s.client.GetOrders(ctx, batch)
		if err != nil {
			return err
		}
		for _, exchangeOrder := range exchangeOrders {
			row, ok := orderRowsByExchangeID[exchangeOrder.OrderID]
			if !ok {
				continue
			}
			if err := s.reconcileOneOrder(ctx, jobRunID, row, exchangeOrder); err != nil {
				return err
			}
		}
	}

	return nil
}

// reconcileOneOrder は 1 件の注文に対して約定取り込みと状態更新を行います。
func (s *Service) reconcileOneOrder(ctx context.Context, jobRunID int64, row store.ListReconcilableOrdersRow, exchangeOrder gmo.Order) error {
	executions, err := s.client.GetExecutions(ctx, exchangeOrder.OrderID)
	if err != nil {
		return s.insertSyncFailure(ctx, jobRunID, row.ID, err)
	}

	totalFee := big.NewRat(0, 1)
	for _, execution := range executions {
		fee, feeErr := parseRat(execution.Fee)
		if feeErr != nil {
			return feeErr
		}
		totalFee.Add(totalFee, absRat(fee))
		if err := s.store.InsertTradeExecution(ctx, store.InsertTradeExecutionParams{
			OrderID:             row.ID,
			ExchangeExecutionID: strconv.FormatInt(execution.ExecutionID, 10),
			ExecutedAt:          pgTime(execution.Timestamp.UTC()),
			PriceJpy:            mustNumeric(execution.Price),
			ExecutedUnits:       mustNumeric(execution.Size),
			FeeJpy:              mustNumeric(absRat(fee).FloatString(8)),
			IsPartialFill:       false,
		}); err != nil {
			return err
		}
	}

	status, err := mapExchangeStatus(exchangeOrder.Status, exchangeOrder.Size, exchangeOrder.ExecutedSize)
	if err != nil {
		return s.insertSyncFailure(ctx, jobRunID, row.ID, err)
	}

	executedUnits, err := parseRat(exchangeOrder.ExecutedSize)
	if err != nil {
		return err
	}
	orderedUnits, err := parseRat(exchangeOrder.Size)
	if err != nil {
		return err
	}
	remainingUnits := new(big.Rat).Sub(orderedUnits, executedUnits)
	if remainingUnits.Sign() < 0 {
		remainingUnits = big.NewRat(0, 1)
	}

	var cancelledAt pgtype.Timestamptz
	if status == "cancelled" {
		cancelledAt = pgTime(s.now().UTC())
	}
	if err := s.store.UpdateOrderAfterSync(ctx, store.UpdateOrderAfterSyncParams{
		ID:             row.ID,
		Status:         status,
		FilledUnits:    mustNumeric(executedUnits.FloatString(8)),
		RemainingUnits: mustNumeric(remainingUnits.FloatString(8)),
		FeeJpy:         mustNumeric(totalFee.FloatString(8)),
		CancelledAt:    cancelledAt,
		CheckedAt:      pgTime(s.now().UTC()),
	}); err != nil {
		return err
	}

	if status == row.Status && executedUnits.FloatString(8) == row.FilledUnits {
		return nil
	}

	eventType := statusToEventType(status, row.FilledUnits, executedUnits.FloatString(8))
	payload, _ := json.Marshal(map[string]any{
		"exchangeStatus": exchangeOrder.Status,
		"executedSize":   exchangeOrder.ExecutedSize,
		"timeInForce":    exchangeOrder.TimeInForce,
		"jobRunId":       jobRunID,
	})
	return s.store.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		OrderID:    row.ID,
		JobRunID:   int64Ptr(jobRunID),
		EventType:  eventType,
		FromStatus: stringPtr(row.Status),
		ToStatus:   stringPtr(status),
		EventAt:    pgTime(s.now().UTC()),
		Payload:    payload,
	})
}

// ensureNoDuplicateDailyTrade は JST 当日の二重実行を防ぎます。
func (s *Service) ensureNoDuplicateDailyTrade(ctx context.Context) error {
	windowStartedAt, windowEndedAt := jstDayWindow(s.now())
	count, err := s.store.CountJobRunsByTypeInWindow(ctx, store.CountJobRunsByTypeInWindowParams{
		JobType:         "daily_trade",
		WindowStartedAt: pgTime(windowStartedAt.UTC()),
		WindowEndedAt:   pgTime(windowEndedAt.UTC()),
	})
	if err != nil {
		return err
	}
	if count > 1 {
		return fmt.Errorf("%w: 当日分の日次売買ジョブは既に実行済みです", ErrJobSkipped)
	}
	return nil
}

// loadDailyTradeInputs は戦略計算に必要な最新残高と最新価格を読み込みます。
func (s *Service) loadDailyTradeInputs(ctx context.Context) (map[string]string, map[string]string, error) {
	balanceRows, err := s.store.ListLatestBalances(ctx)
	if err != nil {
		return nil, nil, err
	}
	priceRows, err := s.store.ListLatestPrices(ctx, nil)
	if err != nil {
		return nil, nil, err
	}

	balances := map[string]string{}
	for _, row := range balanceRows {
		balances[row.AssetCode] = row.AvailableAmount
	}
	prices := map[string]string{}
	for _, row := range priceRows {
		prices[row.AssetCode] = row.PriceJpy
	}

	for _, asset := range []string{"JPY", "BTC", "ETH"} {
		if _, ok := balances[asset]; !ok {
			balances[asset] = "0"
		}
	}
	for _, asset := range []string{"BTC", "ETH"} {
		if prices[asset] == "" {
			return nil, nil, fmt.Errorf("最新価格が存在しません: %s", asset)
		}
	}

	return balances, prices, nil
}

// loadRules は戦略で使う BTC/ETH の GMO 銘柄ルールを map 化します。
func (s *Service) loadRules(ctx context.Context) (map[string]gmo.SymbolRule, error) {
	rules, err := s.client.GetSymbolRules(ctx)
	if err != nil {
		return nil, err
	}

	out := map[string]gmo.SymbolRule{}
	for _, rule := range rules {
		switch rule.Symbol {
		case "BTC_JPY":
			out["BTC"] = rule
		case "ETH_JPY":
			out["ETH"] = rule
		}
	}
	if len(out) != 2 {
		return nil, errors.New("必要な銘柄ルールを取得できませんでした")
	}
	return out, nil
}

// loadWeeklyRemaining は直近 7 日の新規買い累計から残り数量を計算します。
func (s *Service) loadWeeklyRemaining(ctx context.Context) (map[string]string, error) {
	windowStartedAt := s.now().UTC().Add(-7 * 24 * time.Hour)
	rows, err := s.store.ListWeeklyConsumedBuyUnits(ctx, pgTime(windowStartedAt))
	if err != nil {
		return nil, err
	}

	consumed := map[string]string{"BTC": "0", "ETH": "0"}
	for _, row := range rows {
		consumed[row.AssetCode] = row.ConsumedUnits
	}

	out := map[string]string{}
	for _, asset := range []string{"BTC", "ETH"} {
		out[asset] = nonNegative(subDecimal(s.weeklyLimitUnits, consumed[asset]))
	}
	return out, nil
}

// getRule は対象通貨の注文ルールを GMO から取得します。
func (s *Service) getRule(ctx context.Context, assetCode string) (gmo.SymbolRule, error) {
	rules, err := s.client.GetSymbolRules(ctx)
	if err != nil {
		return gmo.SymbolRule{}, err
	}
	target := symbolToPair(assetCode)
	for _, rule := range rules {
		if rule.Symbol == target {
			return rule, nil
		}
	}
	return gmo.SymbolRule{}, fmt.Errorf("symbol rule not found: %s", target)
}

// insertSyncFailure は注文単位の同期失敗を order_events に残します。
func (s *Service) insertSyncFailure(ctx context.Context, jobRunID int64, orderID int64, cause error) error {
	payload, _ := json.Marshal(map[string]string{
		"error": cause.Error(),
	})
	if err := s.store.InsertOrderEvent(ctx, store.InsertOrderEventParams{
		OrderID:   orderID,
		JobRunID:  int64Ptr(jobRunID),
		EventType: "sync_failed",
		EventAt:   pgTime(s.now().UTC()),
		Payload:   payload,
	}); err != nil {
		return err
	}
	return cause
}

// IsNotFound は対象注文が存在しないエラーかどうかを返します。
func IsNotFound(err error) bool {
	return err == pgx.ErrNoRows
}

// symbolToPair は内部の資産コードを GMO の現物ペア名へ変換します。
func symbolToPair(assetCode string) string { return strings.ToUpper(assetCode) + "_JPY" }

// isDryRunExchangeOrderID は dry-run で採番した疑似注文 ID かを判定します。
func isDryRunExchangeOrderID(value string) bool {
	return strings.HasPrefix(value, dryRunExchangeOrderIDPrefix)
}

// validateOrderUnits は最小数量と数量刻みに合っているかを確認します。
func validateOrderUnits(units string, minOrderSize string, sizeStep string) error {
	value, err := parseRat(units)
	if err != nil {
		return fmt.Errorf("%w: units must be decimal", ErrInvalidOrderInput)
	}
	minValue, err := parseRat(minOrderSize)
	if err != nil {
		return err
	}
	stepValue, err := parseRat(sizeStep)
	if err != nil {
		return err
	}

	if value.Cmp(minValue) < 0 {
		return fmt.Errorf("注文数量が最小数量未満です: units=%s min=%s", units, minOrderSize)
	}

	quotient := new(big.Rat).Quo(value, stepValue)
	if quotient.Denom().Cmp(big.NewInt(1)) != 0 {
		return fmt.Errorf("注文数量が刻み幅に一致しません: units=%s step=%s", units, sizeStep)
	}

	return nil
}

func validateCreateInput(assetCode string, side string, priceJpy string, units string, timeInForce string) error {
	switch assetCode {
	case "BTC", "ETH":
	default:
		return fmt.Errorf("%w: unsupported asset code", ErrInvalidOrderInput)
	}

	switch side {
	case "buy", "sell":
	default:
		return fmt.Errorf("%w: unsupported side", ErrInvalidOrderInput)
	}

	switch timeInForce {
	case "SOK", "FAS", "FAK", "FOK":
	default:
		return fmt.Errorf("%w: unsupported timeInForce", ErrInvalidOrderInput)
	}

	price, err := parseRat(priceJpy)
	if err != nil {
		return fmt.Errorf("%w: priceJpy must be decimal", ErrInvalidOrderInput)
	}
	if price.Sign() <= 0 {
		return fmt.Errorf("%w: priceJpy must be positive", ErrInvalidOrderInput)
	}

	unitValue, err := parseRat(units)
	if err != nil {
		return fmt.Errorf("%w: units must be decimal", ErrInvalidOrderInput)
	}
	if unitValue.Sign() <= 0 {
		return fmt.Errorf("%w: units must be positive", ErrInvalidOrderInput)
	}

	return nil
}

// buildSellOrderInput は保有数量の 5% を 105% 指値で売る注文を組み立てます。
func buildSellOrderInput(asset string, availableUnits string, currentPrice string, rule gmo.SymbolRule) (CreateInput, bool, error) {
	units := quantizeDown(mulDecimal(availableUnits, "0.05"), rule.SizeStep)
	if units == "0" || lessThan(units, rule.MinOrderSize) {
		return CreateInput{}, false, nil
	}
	price := quantizeUp(mulDecimal(currentPrice, "1.05"), rule.TickSize)
	return CreateInput{
		AssetCode:   asset,
		Side:        "sell",
		PriceJpy:    price,
		Units:       units,
		TimeInForce: defaultTimeInForce,
		RequestedBy: "daily_trade",
	}, true, nil
}

// buildBuyOrderInput は JPY 残高の 50% を上限に 90% 指値の買い注文を組み立てます。
func buildBuyOrderInput(asset string, availableJpy string, currentPrice string, rule gmo.SymbolRule, weeklyRemaining string) (CreateInput, bool, error) {
	if weeklyRemaining == "0" {
		return CreateInput{}, false, nil
	}

	buyPrice := quantizeDown(mulDecimal(currentPrice, "0.9"), rule.TickSize)
	if buyPrice == "0" {
		return CreateInput{}, false, nil
	}
	maxBudget := mulDecimal(availableJpy, "0.5")
	rawUnits := divDecimal(maxBudget, buyPrice)
	units := quantizeDown(minDecimal(rawUnits, weeklyRemaining), rule.SizeStep)
	if units == "0" || lessThan(units, rule.MinOrderSize) {
		return CreateInput{}, false, nil
	}

	return CreateInput{
		AssetCode:   asset,
		Side:        "buy",
		PriceJpy:    buyPrice,
		Units:       units,
		TimeInForce: defaultTimeInForce,
		RequestedBy: "daily_trade",
	}, true, nil
}

// mapExchangeStatus は GMO の状態をローカル状態へ正規化します。
func mapExchangeStatus(exchangeStatus string, size string, executedSize string) (string, error) {
	executed, err := parseRat(executedSize)
	if err != nil {
		return "", err
	}
	total, err := parseRat(size)
	if err != nil {
		return "", err
	}

	switch exchangeStatus {
	case "WAITING", "ORDERED", "MODIFYING":
		if executed.Sign() > 0 && executed.Cmp(total) < 0 {
			return "partially_filled", nil
		}
		return "open", nil
	case "CANCELLING":
		return "cancel_requested", nil
	case "CANCELED":
		return "cancelled", nil
	case "EXECUTED":
		return "filled", nil
	case "EXPIRED":
		return "expired", nil
	default:
		return "", fmt.Errorf("unsupported exchange status: %s", exchangeStatus)
	}
}

// statusToEventType は同期結果から記録すべきイベント種別を決めます。
func statusToEventType(status string, previousFilledUnits string, filledUnits string) string {
	if status == "filled" {
		return "filled"
	}
	if status == "cancelled" {
		return "cancelled"
	}
	if status == "expired" {
		return "expired"
	}
	if previousFilledUnits != filledUnits {
		return "partial_fill"
	}
	return "opened"
}

// chunkInt64s は GMO API の 10 件制限に合わせて注文 ID を分割します。
func chunkInt64s(values []int64, size int) [][]int64 {
	if len(values) == 0 {
		return nil
	}
	var chunks [][]int64
	for start := 0; start < len(values); start += size {
		end := start + size
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, values[start:end])
	}
	return chunks
}

// jstDayWindow は指定時刻が属する JST 日付の開始・終了 UTC を返します。
func jstDayWindow(now time.Time) (time.Time, time.Time) {
	jst := time.FixedZone("Asia/Tokyo", 9*60*60)
	inJST := now.In(jst)
	start := time.Date(inJST.Year(), inJST.Month(), inJST.Day(), 0, 0, 0, 0, jst)
	return start.UTC(), start.Add(24 * time.Hour).UTC()
}

// parseRat は decimal string を誤差なく扱うため big.Rat へ変換します。
func parseRat(value string) (*big.Rat, error) {
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return nil, fmt.Errorf("invalid decimal: %s", value)
	}
	return rat, nil
}

// mulDecimal は decimal string 同士を掛け算します。
func mulDecimal(left string, right string) string {
	l, _ := parseRat(left)
	r, _ := parseRat(right)
	return new(big.Rat).Mul(l, r).FloatString(8)
}

// divDecimal は decimal string 同士を割り算します。
func divDecimal(left string, right string) string {
	l, _ := parseRat(left)
	r, _ := parseRat(right)
	if r.Sign() == 0 {
		return "0"
	}
	return new(big.Rat).Quo(l, r).FloatString(8)
}

// subDecimal は decimal string 同士を減算します。
func subDecimal(left string, right string) string {
	l, _ := parseRat(left)
	r, _ := parseRat(right)
	return new(big.Rat).Sub(l, r).FloatString(8)
}

// minDecimal はより小さい値を返します。
func minDecimal(left string, right string) string {
	l, _ := parseRat(left)
	r, _ := parseRat(right)
	if l.Cmp(r) <= 0 {
		return l.FloatString(8)
	}
	return r.FloatString(8)
}

// lessThan は left < right かを判定します。
func lessThan(left string, right string) bool {
	l, _ := parseRat(left)
	r, _ := parseRat(right)
	return l.Cmp(r) < 0
}

// quantizeDown は step に合わせて切り捨てます。
func quantizeDown(value string, step string) string {
	v, _ := parseRat(value)
	s, _ := parseRat(step)
	if s.Sign() == 0 {
		return v.FloatString(8)
	}
	q := new(big.Rat).Quo(v, s)
	if q.Denom().Cmp(big.NewInt(1)) == 0 {
		return v.FloatString(8)
	}
	intPart := new(big.Int).Quo(q.Num(), q.Denom())
	return new(big.Rat).Mul(new(big.Rat).SetInt(intPart), s).FloatString(8)
}

// quantizeUp は step に合わせて切り上げます。
func quantizeUp(value string, step string) string {
	down := quantizeDown(value, step)
	if down == value {
		return down
	}
	d, _ := parseRat(down)
	s, _ := parseRat(step)
	return new(big.Rat).Add(d, s).FloatString(8)
}

// nonNegative は負値を 0 に丸めます。
func nonNegative(value string) string {
	v, _ := parseRat(value)
	if v.Sign() < 0 {
		return "0"
	}
	return v.FloatString(8)
}

// absRat は fee などの符号を正に寄せます。
func absRat(value *big.Rat) *big.Rat {
	if value.Sign() < 0 {
		return new(big.Rat).Neg(value)
	}
	return new(big.Rat).Set(value)
}

// mustNumeric は decimal string を sqlc 用の pgtype.Numeric へ変換します。
func mustNumeric(value string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.ScanScientific(value); err != nil {
		panic(err)
	}
	return n
}

// pgUUID は UUID を sqlc 用の nullable 型へ詰めます。
func pgUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: value, Valid: true}
}

// pgTime は時刻を sqlc 用の nullable 型へ詰めます。
func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

// stringPtr は nullable string 用の補助関数です。
func stringPtr(value string) *string {
	return &value
}

// int64Ptr は nullable bigint 用の補助関数です。
func int64Ptr(value int64) *int64 {
	return &value
}
