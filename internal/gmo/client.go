package gmo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	publicEndpoint  = "https://api.coin.z.com/public"
	privateEndpoint = "https://api.coin.z.com/private"
)

type Client struct {
	apiKey         string
	secretKey      string
	httpClient     *http.Client
	publicBaseURL  string
	privateBaseURL string
	now            func() time.Time
}

type Asset struct {
	Symbol         string
	Amount         string
	Available      string
	ConversionRate string
}

type Ticker struct {
	Symbol    string
	Last      string
	Timestamp time.Time
}

// SymbolRule は GMO の公開APIから取得する現物銘柄ルールです。
type SymbolRule struct {
	Symbol       string
	MinOrderSize string
	MaxOrderSize string
	SizeStep     string
	TickSize     string
	TakerFee     string
	MakerFee     string
}

// Order は GMO 側の注文状態を同期するための最小表現です。
type Order struct {
	OrderID      int64
	Symbol       string
	Side         string
	Size         string
	ExecutedSize string
	Price        string
	Status       string
	TimeInForce  string
	Timestamp    time.Time
}

// Execution は GMO 側の約定情報を保持します。
type Execution struct {
	ExecutionID int64
	OrderID     int64
	Symbol      string
	Side        string
	Size        string
	Price       string
	Fee         string
	Timestamp   time.Time
}

// CreateOrderRequest は現時点で対応している現物指値注文ペイロードです。
type CreateOrderRequest struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	ExecutionType string `json:"executionType"`
	TimeInForce   string `json:"timeInForce,omitempty"`
	Price         string `json:"price,omitempty"`
	Size          string `json:"size"`
}

// CreateOrderResponse は GMO が返す注文IDだけを保持します。
type CreateOrderResponse struct {
	OrderID string
}

type apiResponse[T any] struct {
	Status       int    `json:"status"`
	Data         T      `json:"data"`
	ResponseTime string `json:"responsetime"`
	Messages     []struct {
		MessageCode string `json:"message_code"`
		Message     string `json:"message_string"`
	} `json:"messages"`
}

type assetsResponse struct {
	Symbol         string `json:"symbol"`
	Amount         string `json:"amount"`
	Available      string `json:"available"`
	ConversionRate string `json:"conversionRate"`
}

type tickerResponse struct {
	Symbol    string    `json:"symbol"`
	Last      string    `json:"last"`
	Timestamp time.Time `json:"timestamp"`
}

type symbolsResponse struct {
	Symbol       string `json:"symbol"`
	MinOrderSize string `json:"minOrderSize"`
	MaxOrderSize string `json:"maxOrderSize"`
	SizeStep     string `json:"sizeStep"`
	TickSize     string `json:"tickSize"`
	TakerFee     string `json:"takerFee"`
	MakerFee     string `json:"makerFee"`
}

type orderResponse struct {
	Data string `json:"data"`
}

type ordersEnvelope struct {
	List []orderStatusResponse `json:"list"`
}

type orderStatusResponse struct {
	OrderID      int64     `json:"orderId"`
	Symbol       string    `json:"symbol"`
	Side         string    `json:"side"`
	Size         string    `json:"size"`
	ExecutedSize string    `json:"executedSize"`
	Price        string    `json:"price"`
	Status       string    `json:"status"`
	TimeInForce  string    `json:"timeInForce"`
	Timestamp    time.Time `json:"timestamp"`
}

type executionsEnvelope struct {
	List []executionResponse `json:"list"`
}

type executionResponse struct {
	ExecutionID int64     `json:"executionId"`
	OrderID     int64     `json:"orderId"`
	Symbol      string    `json:"symbol"`
	Side        string    `json:"side"`
	Size        string    `json:"size"`
	Price       string    `json:"price"`
	Fee         string    `json:"fee"`
	Timestamp   time.Time `json:"timestamp"`
}

type cancelOrderRequest struct {
	OrderID int64 `json:"orderId"`
}

// NewClient は GMO 用の HTTP クライアントを初期化します。
func NewClient(apiKey string, secretKey string) *Client {
	return &Client{
		apiKey:    apiKey,
		secretKey: secretKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		publicBaseURL:  publicEndpoint,
		privateBaseURL: privateEndpoint,
		now:            time.Now,
	}
}

// GetAssets は認証付き API から資産残高を取得します。
func (c *Client) GetAssets(ctx context.Context) ([]Asset, error) {
	var resp apiResponse[[]assetsResponse]
	if err := c.privateGet(ctx, "/v1/account/assets", nil, &resp); err != nil {
		return nil, err
	}

	assets := make([]Asset, 0, len(resp.Data))
	for _, item := range resp.Data {
		assets = append(assets, Asset{
			Symbol:         item.Symbol,
			Amount:         item.Amount,
			Available:      item.Available,
			ConversionRate: item.ConversionRate,
		})
	}

	return assets, nil
}

// GetTicker は公開 API から指定銘柄の最新価格を取得します。
func (c *Client) GetTicker(ctx context.Context, symbol string) (Ticker, error) {
	query := url.Values{}
	query.Set("symbol", symbol)

	var resp apiResponse[[]tickerResponse]
	if err := c.publicGet(ctx, "/v1/ticker", query, &resp); err != nil {
		return Ticker{}, err
	}
	if len(resp.Data) == 0 {
		return Ticker{}, fmt.Errorf("gmo ticker response was empty for symbol=%s", symbol)
	}

	item := resp.Data[0]
	return Ticker{
		Symbol:    item.Symbol,
		Last:      item.Last,
		Timestamp: item.Timestamp,
	}, nil
}

// GetSymbolRules は銘柄ごとの最小数量や刻み幅を取得します。
func (c *Client) GetSymbolRules(ctx context.Context) ([]SymbolRule, error) {
	var resp apiResponse[[]symbolsResponse]
	if err := c.publicGet(ctx, "/v1/symbols", nil, &resp); err != nil {
		return nil, err
	}

	rules := make([]SymbolRule, 0, len(resp.Data))
	for _, item := range resp.Data {
		rules = append(rules, SymbolRule{
			Symbol:       item.Symbol,
			MinOrderSize: item.MinOrderSize,
			MaxOrderSize: item.MaxOrderSize,
			SizeStep:     item.SizeStep,
			TickSize:     item.TickSize,
			TakerFee:     item.TakerFee,
			MakerFee:     item.MakerFee,
		})
	}

	return rules, nil
}

// GetOrders は指定した注文 ID 群の最新状態を取得します。
func (c *Client) GetOrders(ctx context.Context, orderIDs []int64) ([]Order, error) {
	query := url.Values{}
	query.Set("orderId", joinInt64s(orderIDs))

	var resp apiResponse[ordersEnvelope]
	if err := c.privateGet(ctx, "/v1/orders", query, &resp); err != nil {
		return nil, err
	}

	orders := make([]Order, 0, len(resp.Data.List))
	for _, item := range resp.Data.List {
		orders = append(orders, Order{
			OrderID:      item.OrderID,
			Symbol:       item.Symbol,
			Side:         item.Side,
			Size:         item.Size,
			ExecutedSize: item.ExecutedSize,
			Price:        item.Price,
			Status:       item.Status,
			TimeInForce:  item.TimeInForce,
			Timestamp:    item.Timestamp,
		})
	}

	return orders, nil
}

// GetExecutions は指定注文の約定一覧を取得します。
func (c *Client) GetExecutions(ctx context.Context, orderID int64) ([]Execution, error) {
	query := url.Values{}
	query.Set("orderId", strconv.FormatInt(orderID, 10))

	var resp apiResponse[executionsEnvelope]
	if err := c.privateGet(ctx, "/v1/executions", query, &resp); err != nil {
		return nil, err
	}

	executions := make([]Execution, 0, len(resp.Data.List))
	for _, item := range resp.Data.List {
		executions = append(executions, Execution{
			ExecutionID: item.ExecutionID,
			OrderID:     item.OrderID,
			Symbol:      item.Symbol,
			Side:        item.Side,
			Size:        item.Size,
			Price:       item.Price,
			Fee:         item.Fee,
			Timestamp:   item.Timestamp,
		})
	}

	return executions, nil
}

// CreateOrder は GMO に新規注文を送信し、注文 ID を返します。
func (c *Client) CreateOrder(ctx context.Context, reqBody CreateOrderRequest) (CreateOrderResponse, error) {
	var resp apiResponse[orderResponse]
	if err := c.privatePost(ctx, "/v1/order", reqBody, &resp); err != nil {
		return CreateOrderResponse{}, err
	}

	return CreateOrderResponse{OrderID: resp.Data.Data}, nil
}

// CancelOrder は GMO 側の注文取消を要求します。
func (c *Client) CancelOrder(ctx context.Context, orderID int64) error {
	var resp apiResponse[struct{}]
	if err := c.privatePost(ctx, "/v1/cancelOrder", cancelOrderRequest{OrderID: orderID}, &resp); err != nil {
		return err
	}

	return nil
}

// publicGet は公開 API 向けの GET リクエストを送ります。
func (c *Client) publicGet(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.publicBaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	return c.do(req, out)
}

// privateGet は認証付き GET リクエストを送ります。
func (c *Client) privateGet(ctx context.Context, path string, query url.Values, out any) error {
	endpoint := c.privateBaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)
	signPayload := timestamp + http.MethodGet + path
	signature := sign(c.secretKey, signPayload)

	req.Header.Set("API-KEY", c.apiKey)
	req.Header.Set("API-TIMESTAMP", timestamp)
	req.Header.Set("API-SIGN", signature)

	return c.do(req, out)
}

// privatePost は認証付き JSON POST リクエストを送ります。
func (c *Client) privatePost(ctx context.Context, path string, body any, out any) error {
	reqBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.privateBaseURL+path, strings.NewReader(string(reqBody)))
	if err != nil {
		return err
	}

	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)
	signPayload := timestamp + http.MethodPost + path + string(reqBody)
	signature := sign(c.secretKey, signPayload)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-KEY", c.apiKey)
	req.Header.Set("API-TIMESTAMP", timestamp)
	req.Header.Set("API-SIGN", signature)

	return c.do(req, out)
}

// do は GMO 共通レスポンスを解釈して呼び出し元の型へ展開します。
func (c *Client) do(req *http.Request, out any) error {
	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("gmo api http error: status=%d body=%s", res.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode gmo response: %w", err)
	}

	switch v := out.(type) {
	case *apiResponse[[]assetsResponse]:
		if v.Status != 0 {
			return fmt.Errorf("gmo assets error: status=%d messages=%v", v.Status, v.Messages)
		}
	case *apiResponse[[]symbolsResponse]:
		if v.Status != 0 {
			return fmt.Errorf("gmo symbols error: status=%d messages=%v", v.Status, v.Messages)
		}
	case *apiResponse[[]tickerResponse]:
		if v.Status != 0 {
			return fmt.Errorf("gmo ticker error: status=%d messages=%v", v.Status, v.Messages)
		}
	case *apiResponse[ordersEnvelope]:
		if v.Status != 0 {
			return fmt.Errorf("gmo orders error: status=%d messages=%v", v.Status, v.Messages)
		}
	case *apiResponse[executionsEnvelope]:
		if v.Status != 0 {
			return fmt.Errorf("gmo executions error: status=%d messages=%v", v.Status, v.Messages)
		}
	case *apiResponse[orderResponse]:
		if v.Status != 0 {
			return fmt.Errorf("gmo create order error: status=%d messages=%v", v.Status, v.Messages)
		}
	case *apiResponse[struct{}]:
		if v.Status != 0 {
			return fmt.Errorf("gmo cancel order error: status=%d messages=%v", v.Status, v.Messages)
		}
	}

	return nil
}

// joinInt64s は GMO の CSV クエリ形式へ注文 ID 群を変換します。
func joinInt64s(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return strings.Join(parts, ",")
}

// sign は GMO の署名文字列を HMAC-SHA256 で計算します。
func sign(secret string, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
