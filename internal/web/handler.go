package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/isksss/cryptoBot/internal/order"
	"github.com/isksss/cryptoBot/internal/store"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

type priceSyncer interface {
	SyncPriceAndBalances(ctx context.Context, requestedBy string, reason string) (int64, error)
}

type orderService interface {
	CreateLimitOrder(ctx context.Context, input order.CreateInput) (store.InsertOrderRow, error)
	CancelOrder(ctx context.Context, localOrderID int64) error
	DailyTrade(ctx context.Context, requestedBy string, reason string) (int64, error)
	ReconcileOrders(ctx context.Context, requestedBy string, reason string) (int64, error)
}

// Handler は Go template と htmx で構成する管理画面を提供します。
type Handler struct {
	queries          store.Querier
	priceSyncer      priceSyncer
	orderService     orderService
	weeklyLimitUnits string
	templates        *template.Template
	mux              *http.ServeMux
	now              func() time.Time
	dryRun           bool
}

type pageData struct {
	DryRun bool
}

type flashData struct {
	Kind    string
	Message string
}

const webRequestedBy = "web-ui"

type summaryData struct {
	ServerTime            string
	OpenOrders            int64
	UnresolvedPreviousDay int64
	WeeklyRemaining       string
}

type balanceRow struct {
	AssetCode       string
	AvailableAmount string
	LockedAmount    string
}

type balancesData struct {
	Rows []balanceRow
}

type priceRow struct {
	AssetCode  string
	PriceJpy   string
	CapturedAt string
}

type pricesData struct {
	Rows []priceRow
}

type orderRow struct {
	ID           int64
	AssetCode    string
	Side         string
	PriceJpy     string
	OrderedUnits string
	Status       string
	Cancelable   bool
}

type ordersData struct {
	Rows []orderRow
}

type jobRow struct {
	ID         int64
	JobType    string
	Status     string
	StartedAt  string
	FinishedAt string
}

type jobsData struct {
	Rows []jobRow
}

// NewHandler は画面描画と htmx 部分更新用の handler を初期化します。
func NewHandler(
	queries store.Querier,
	priceSyncer priceSyncer,
	orderService orderService,
	weeklyLimitUnits string,
	dryRun bool,
) (*Handler, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}

	h := &Handler{
		queries:          queries,
		priceSyncer:      priceSyncer,
		orderService:     orderService,
		weeklyLimitUnits: weeklyLimitUnits,
		templates:        tmpl,
		mux:              http.NewServeMux(),
		now:              time.Now,
		dryRun:           dryRun,
	}
	h.routes()
	return h, nil
}

// ServeHTTP は管理画面用ルーティングを処理します。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// routes は管理画面のルートと htmx 部分更新のルートを登録します。
func (h *Handler) routes() {
	h.mux.HandleFunc("GET /", h.index)
	h.mux.HandleFunc("GET /ui/partials/summary", h.summary)
	h.mux.HandleFunc("GET /ui/partials/balances", h.balances)
	h.mux.HandleFunc("GET /ui/partials/prices", h.prices)
	h.mux.HandleFunc("GET /ui/partials/orders", h.orders)
	h.mux.HandleFunc("GET /ui/partials/jobs", h.jobs)
	h.mux.HandleFunc("POST /ui/actions/price-fetch", h.triggerPriceFetch)
	h.mux.HandleFunc("POST /ui/actions/order-reconcile", h.triggerOrderReconcile)
	h.mux.HandleFunc("POST /ui/actions/daily-trade", h.triggerDailyTrade)
	h.mux.HandleFunc("POST /ui/actions/orders", h.createOrder)
	h.mux.HandleFunc("POST /ui/actions/orders/{orderId}/cancel", h.cancelOrder)
}

// index は管理画面の親ページを返します。
func (h *Handler) index(w http.ResponseWriter, _ *http.Request) {
	h.render(w, "page", pageData{DryRun: h.dryRun})
}

// summary はダッシュボードの要約カードを返します。
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	openOrders, err := h.queries.CountOpenOrders(r.Context())
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}
	unresolved, err := h.queries.CountUnresolvedPreviousDayOrders(r.Context())
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}
	remaining, err := h.loadWeeklyRemaining(r.Context())
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	h.render(w, "summary", summaryData{
		ServerTime:            h.now().In(jst()).Format(time.DateTime),
		OpenOrders:            openOrders,
		UnresolvedPreviousDay: unresolved,
		WeeklyRemaining:       remaining,
	})
}

// balances は最新残高の表を返します。
func (h *Handler) balances(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListLatestBalances(r.Context())
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	data := balancesData{Rows: make([]balanceRow, 0, len(rows))}
	for _, row := range rows {
		data.Rows = append(data.Rows, balanceRow{
			AssetCode:       row.AssetCode,
			AvailableAmount: row.AvailableAmount,
			LockedAmount:    row.LockedAmount,
		})
	}
	h.render(w, "balances", data)
}

// prices は最新価格の表を返します。
func (h *Handler) prices(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListLatestPrices(r.Context(), nil)
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	data := pricesData{Rows: make([]priceRow, 0, len(rows))}
	for _, row := range rows {
		data.Rows = append(data.Rows, priceRow{
			AssetCode:  row.AssetCode,
			PriceJpy:   row.PriceJpy,
			CapturedAt: row.CapturedAt.Time.In(jst()).Format(time.DateTime),
		})
	}
	h.render(w, "prices", data)
}

// orders は直近注文一覧と取消ボタンを返します。
func (h *Handler) orders(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListOrders(r.Context(), store.ListOrdersParams{LimitCount: 20})
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	data := ordersData{Rows: make([]orderRow, 0, len(rows))}
	for _, row := range rows {
		data.Rows = append(data.Rows, orderRow{
			ID:           row.ID,
			AssetCode:    row.AssetCode,
			Side:         row.Side,
			PriceJpy:     row.PriceJpy,
			OrderedUnits: row.OrderedUnits,
			Status:       row.Status,
			Cancelable:   row.Status == "open" || row.Status == "partially_filled" || row.Status == "cancel_requested",
		})
	}
	h.render(w, "orders", data)
}

// jobs は直近ジョブ実行履歴を返します。
func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListLatestJobRuns(r.Context(), 20)
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	data := jobsData{Rows: make([]jobRow, 0, len(rows))}
	for _, row := range rows {
		finished := "-"
		if row.FinishedAt.Valid {
			finished = row.FinishedAt.Time.In(jst()).Format(time.DateTime)
		}
		data.Rows = append(data.Rows, jobRow{
			ID:         row.ID,
			JobType:    row.JobType,
			Status:     row.Status,
			StartedAt:  row.StartedAt.Time.In(jst()).Format(time.DateTime),
			FinishedAt: finished,
		})
	}
	h.render(w, "jobs", data)
}

// triggerPriceFetch は価格同期を起動し、結果をフラッシュ表示します。
func (h *Handler) triggerPriceFetch(w http.ResponseWriter, r *http.Request) {
	if _, err := h.priceSyncer.SyncPriceAndBalances(r.Context(), "web-ui", "管理画面からの手動同期"); err != nil {
		h.writeErrorFlash(w, err)
		return
	}
	h.writeSuccessFlash(w, "価格同期を起動しました")
}

// triggerOrderReconcile は注文状態同期を起動し、結果をフラッシュ表示します。
func (h *Handler) triggerOrderReconcile(w http.ResponseWriter, r *http.Request) {
	if _, err := h.orderService.ReconcileOrders(r.Context(), "web-ui", "管理画面からの手動注文同期"); err != nil {
		h.writeErrorFlash(w, err)
		return
	}
	h.writeSuccessFlash(w, "注文同期を起動しました")
}

// triggerDailyTrade は日次売買ジョブを起動し、結果をフラッシュ表示します。
func (h *Handler) triggerDailyTrade(w http.ResponseWriter, r *http.Request) {
	if _, err := h.orderService.DailyTrade(r.Context(), "web-ui", "管理画面からの日次売買実行"); err != nil {
		h.writeErrorFlash(w, err)
		return
	}
	h.writeSuccessFlash(w, "日次売買ジョブを起動しました")
}

// createOrder は画面フォームから新規注文を作成します。
func (h *Handler) createOrder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	assetCode := r.FormValue("assetCode")
	side := r.FormValue("side")
	priceJpy := strings.TrimSpace(r.FormValue("priceJpy"))
	units := strings.TrimSpace(r.FormValue("units"))
	timeInForce := r.FormValue("timeInForce")
	if err := validateOrderForm(assetCode, side, priceJpy, units, timeInForce); err != nil {
		h.writeErrorFlashWithStatus(w, http.StatusBadRequest, err)
		return
	}

	_, err := h.orderService.CreateLimitOrder(r.Context(), order.CreateInput{
		AssetCode:   assetCode,
		Side:        side,
		PriceJpy:    priceJpy,
		Units:       units,
		TimeInForce: timeInForce,
		RequestedBy: webRequestedBy,
	})
	if err != nil {
		if errors.Is(err, order.ErrInvalidOrderInput) {
			h.writeErrorFlashWithStatus(w, http.StatusBadRequest, err)
			return
		}
		h.writeErrorFlash(w, err)
		return
	}

	h.writeSuccessFlash(w, "注文を作成しました")
}

// cancelOrder は画面からの取消要求を注文サービスへ渡します。
func (h *Handler) cancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := strconv.ParseInt(r.PathValue("orderId"), 10, 64)
	if err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	if err := h.orderService.CancelOrder(r.Context(), orderID); err != nil {
		h.writeErrorFlash(w, err)
		return
	}

	h.writeSuccessFlash(w, "注文取消を受け付けました")
}

// loadWeeklyRemaining はダッシュボード表示用に BTC/ETH の残上限を合算表示します。
func (h *Handler) loadWeeklyRemaining(ctx context.Context) (string, error) {
	rows, err := h.queries.ListWeeklyConsumedBuyUnits(ctx, pgTime(h.now().UTC().Add(-7*24*time.Hour)))
	if err != nil {
		return "", err
	}

	consumed := map[string]string{"BTC": "0", "ETH": "0"}
	for _, row := range rows {
		consumed[row.AssetCode] = row.ConsumedUnits
	}

	return "BTC " + nonNegative(subDecimal(h.weeklyLimitUnits, consumed["BTC"])) +
		" / ETH " + nonNegative(subDecimal(h.weeklyLimitUnits, consumed["ETH"])), nil
}

// writeSuccessFlash は成功メッセージと部分更新トリガーを返します。
func (h *Handler) writeSuccessFlash(w http.ResponseWriter, message string) {
	w.Header().Set("HX-Trigger", "refresh-all")
	h.render(w, "flash", flashData{Kind: "success", Message: message})
}

// writeErrorFlash は画面用のエラーメッセージを返します。
func (h *Handler) writeErrorFlash(w http.ResponseWriter, err error) {
	h.writeErrorFlashWithStatus(w, http.StatusInternalServerError, err)
}

func (h *Handler) writeErrorFlashWithStatus(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = h.templates.ExecuteTemplate(w, "flash", flashData{Kind: "error", Message: err.Error()})
}

// render は HTML テンプレートを実行します。
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.templates.ExecuteTemplate(w, name, data)
}

// valueOrDefault は空文字を既定値で置き換えます。
func valueOrDefault(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func validateOrderForm(assetCode string, side string, priceJpy string, units string, timeInForce string) error {
	if assetCode != "BTC" && assetCode != "ETH" {
		return fmt.Errorf("assetCode must be BTC or ETH")
	}
	if side != "buy" && side != "sell" {
		return fmt.Errorf("side must be buy or sell")
	}
	if timeInForce != "SOK" && timeInForce != "FAS" && timeInForce != "FAK" && timeInForce != "FOK" {
		return fmt.Errorf("timeInForce must be SOK, FAS, FAK, or FOK")
	}
	if _, ok := new(big.Rat).SetString(priceJpy); !ok {
		return fmt.Errorf("priceJpy must be decimal")
	}
	if _, ok := new(big.Rat).SetString(units); !ok {
		return fmt.Errorf("units must be decimal")
	}
	if isZeroOrNegative(priceJpy) {
		return fmt.Errorf("priceJpy must be positive")
	}
	if isZeroOrNegative(units) {
		return fmt.Errorf("units must be positive")
	}
	return nil
}

func isZeroOrNegative(value string) bool {
	r, ok := new(big.Rat).SetString(value)
	return !ok || r.Sign() <= 0
}

// jst は画面表示で共通利用する JST ロケーションです。
func jst() *time.Location {
	return time.FixedZone("Asia/Tokyo", 9*60*60)
}

// pgTime は sqlc 向けの timestamptz 値を作ります。
func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

// subDecimal は文字列小数の差を返します。
func subDecimal(left string, right string) string {
	l, _ := new(big.Rat).SetString(left)
	r, _ := new(big.Rat).SetString(right)
	if l == nil || r == nil {
		return "0"
	}
	return new(big.Rat).Sub(l, r).FloatString(8)
}

// nonNegative は負の値を 0 に丸めます。
func nonNegative(value string) string {
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return "0"
	}
	return r.FloatString(8)
}
