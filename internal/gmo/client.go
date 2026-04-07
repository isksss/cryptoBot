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
	case *apiResponse[[]tickerResponse]:
		if v.Status != 0 {
			return fmt.Errorf("gmo ticker error: status=%d messages=%v", v.Status, v.Messages)
		}
	}

	return nil
}

func sign(secret string, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
