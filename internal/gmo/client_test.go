package gmo

import (
	"context"
	"encoding/json"
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
