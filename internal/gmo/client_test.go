package gmo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestGetTicker(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ticker" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("symbol"); got != "BTC" {
			t.Fatalf("unexpected symbol: %s", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": []map[string]any{
				{
					"symbol":    "BTC",
					"last":      "12345678",
					"timestamp": "2026-04-07T00:00:00.000Z",
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.publicBaseURL = ts.URL

	ticker, err := client.GetTicker(context.Background(), "BTC")
	if err != nil {
		t.Fatalf("GetTicker returned error: %v", err)
	}
	if ticker.Symbol != "BTC" {
		t.Fatalf("unexpected symbol: %s", ticker.Symbol)
	}
	if ticker.Last != "12345678" {
		t.Fatalf("unexpected last: %s", ticker.Last)
	}
	if ticker.Timestamp.IsZero() {
		t.Fatal("expected timestamp to be parsed")
	}
}

func TestGetAssetsSignsRequest(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	wantTimestamp := "1775563200000"
	wantSign := sign("secret", wantTimestamp+http.MethodGet+"/v1/account/assets")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/assets" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("API-KEY"); got != "key" {
			t.Fatalf("unexpected api key: %s", got)
		}
		if got := r.Header.Get("API-TIMESTAMP"); got != wantTimestamp {
			t.Fatalf("unexpected timestamp: %s", got)
		}
		if got := r.Header.Get("API-SIGN"); got != wantSign {
			t.Fatalf("unexpected sign: %s", got)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": []map[string]any{
				{
					"symbol":         "BTC",
					"amount":         "0.20000000",
					"available":      "0.15000000",
					"conversionRate": "10000000",
				},
				{
					"symbol":         "ETH",
					"amount":         "1.00000000",
					"available":      "0.50000000",
					"conversionRate": "300000",
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.privateBaseURL = ts.URL
	client.now = func() time.Time { return fixedNow }

	assets, err := client.GetAssets(context.Background())
	if err != nil {
		t.Fatalf("GetAssets returned error: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("unexpected asset count: %d", len(assets))
	}
	if assets[0].Symbol != "BTC" || assets[0].Available != "0.15000000" {
		t.Fatalf("unexpected first asset: %+v", assets[0])
	}
}

func TestPublicGetEncodesQuery(t *testing.T) {
	t.Parallel()

	expectedQuery := url.Values{"symbol": []string{"ETH"}}.Encode()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RawQuery; got != expectedQuery {
			t.Fatalf("unexpected raw query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": []map[string]any{
				{
					"symbol":    "ETH",
					"last":      "345678",
					"timestamp": "2026-04-07T00:00:00.000Z",
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.publicBaseURL = ts.URL

	if _, err := client.GetTicker(context.Background(), "ETH"); err != nil {
		t.Fatalf("GetTicker returned error: %v", err)
	}
}

func TestGetSymbolRules(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/symbols" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": []map[string]any{
				{
					"symbol":       "BTC_JPY",
					"minOrderSize": "0.001",
					"maxOrderSize": "5",
					"sizeStep":     "0.001",
					"tickSize":     "1",
					"takerFee":     "0",
					"makerFee":     "0",
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.publicBaseURL = ts.URL

	rules, err := client.GetSymbolRules(context.Background())
	if err != nil {
		t.Fatalf("GetSymbolRules returned error: %v", err)
	}
	if len(rules) != 1 || rules[0].MinOrderSize != "0.001" {
		t.Fatalf("unexpected rules: %+v", rules)
	}
}

func TestGetOrders(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("orderId"); got != "1,2" {
			t.Fatalf("unexpected orderId query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": map[string]any{
				"list": []map[string]any{
					{
						"orderId":      1,
						"symbol":       "BTC_JPY",
						"side":         "BUY",
						"size":         "0.001",
						"executedSize": "0",
						"price":        "10000000",
						"status":       "ORDERED",
						"timeInForce":  "SOK",
						"timestamp":    "2026-04-08T00:00:00.000Z",
					},
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.privateBaseURL = ts.URL

	orders, err := client.GetOrders(context.Background(), []int64{1, 2})
	if err != nil {
		t.Fatalf("GetOrders returned error: %v", err)
	}
	if len(orders) != 1 || orders[0].OrderID != 1 {
		t.Fatalf("unexpected orders: %+v", orders)
	}
}

func TestGetExecutions(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("orderId"); got != "1" {
			t.Fatalf("unexpected orderId query: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": map[string]any{
				"list": []map[string]any{
					{
						"executionId": 10,
						"orderId":     1,
						"symbol":      "BTC_JPY",
						"side":        "BUY",
						"size":        "0.001",
						"price":       "10000000",
						"fee":         "0",
						"timestamp":   "2026-04-08T00:01:00.000Z",
					},
				},
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.privateBaseURL = ts.URL

	executions, err := client.GetExecutions(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetExecutions returned error: %v", err)
	}
	if len(executions) != 1 || executions[0].ExecutionID != 10 {
		t.Fatalf("unexpected executions: %+v", executions)
	}
}

func TestCreateOrderSignsRequest(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	requestBody := `{"symbol":"BTC_JPY","side":"BUY","executionType":"LIMIT","timeInForce":"SOK","price":"10000000","size":"0.01"}`
	wantTimestamp := "1775563200000"
	wantSign := sign("secret", wantTimestamp+http.MethodPost+"/v1/order"+requestBody)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/order" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("API-SIGN"); got != wantSign {
			t.Fatalf("unexpected sign: %s", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if string(body) != requestBody {
			t.Fatalf("unexpected body: %s", string(body))
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data": map[string]any{
				"data": "12345",
			},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.privateBaseURL = ts.URL
	client.now = func() time.Time { return fixedNow }

	resp, err := client.CreateOrder(context.Background(), CreateOrderRequest{
		Symbol:        "BTC_JPY",
		Side:          "BUY",
		ExecutionType: "LIMIT",
		TimeInForce:   "SOK",
		Price:         "10000000",
		Size:          "0.01",
	})
	if err != nil {
		t.Fatalf("CreateOrder returned error: %v", err)
	}
	if resp.OrderID != "12345" {
		t.Fatalf("unexpected order id: %s", resp.OrderID)
	}
}

func TestCancelOrderSignsRequest(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	requestBody := `{"orderId":12345}`
	wantTimestamp := "1775563200000"
	wantSign := sign("secret", wantTimestamp+http.MethodPost+"/v1/cancelOrder"+requestBody)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/cancelOrder" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("API-SIGN"); got != wantSign {
			t.Fatalf("unexpected sign: %s", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if string(body) != requestBody {
			t.Fatalf("unexpected body: %s", string(body))
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0,
			"data":   map[string]any{},
		})
	}))
	defer ts.Close()

	client := NewClient("key", "secret")
	client.privateBaseURL = ts.URL
	client.now = func() time.Time { return fixedNow }

	if err := client.CancelOrder(context.Background(), 12345); err != nil {
		t.Fatalf("CancelOrder returned error: %v", err)
	}
}
