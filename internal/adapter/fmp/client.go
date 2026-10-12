package fmp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/hisho-123/stock_notification_backend/internal/domain"
	"github.com/hisho-123/stock_notification_backend/internal/usecase"
)

const endpoint = "https://financialmodelingprep.com/stable/quote"

type Client struct {
	APIKey, Endpoint string
	HTTP             *http.Client
	Now              func() time.Time
}

func New(apiKey string, client *http.Client) *Client {
	h := http.Client{}
	if client != nil {
		h = *client
	}
	if h.Timeout <= 0 || h.Timeout > 30*time.Second {
		h.Timeout = 30 * time.Second
	}
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{APIKey: apiKey, Endpoint: endpoint, HTTP: &h, Now: time.Now}
}
func (c *Client) Fetch(ctx context.Context, code string) (domain.Quote, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return domain.Quote{}, usecase.ErrPriceProvider
	}
	query := req.URL.Query()
	query.Set("symbol", code)
	query.Set("apikey", c.APIKey)
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return domain.Quote{}, providerError(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return domain.Quote{}, usecase.ErrPriceProvider
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return domain.Quote{}, providerError(err)
	}
	if len(body) > maxBody {
		return domain.Quote{}, usecase.ErrPriceProvider
	}
	fetchedAt := c.Now().UTC()
	var quotes []struct {
		Symbol    string          `json:"symbol"`
		Name      json.RawMessage `json:"name"`
		Price     json.RawMessage `json:"price"`
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if err := json.Unmarshal(body, &quotes); err != nil || quotes == nil {
		return domain.Quote{}, usecase.ErrPriceProvider
	}
	for _, q := range quotes {
		if q.Symbol != code {
			continue
		}
		price, err := domain.ParsePrice(string(q.Price))
		if err != nil {
			return domain.Quote{}, usecase.ErrPriceProvider
		}
		seconds, err := strconv.ParseInt(string(q.Timestamp), 10, 64)
		if err != nil {
			return domain.Quote{}, usecase.ErrPriceProvider
		}
		providedAt := time.Unix(seconds, 0).UTC()
		if providedAt.Year() < 1 || providedAt.Year() > 9999 {
			return domain.Quote{}, usecase.ErrPriceProvider
		}
		var name string
		_ = json.Unmarshal(q.Name, &name)
		return domain.Quote{Price: price, Name: domain.NormalizeName(name, code), ProvidedAt: providedAt, FetchedAt: fetchedAt}, nil
	}
	return domain.Quote{}, usecase.ErrSymbolUnavailable
}
func providerError(err error) error {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		return usecase.ErrPriceTimeout
	}
	return usecase.ErrPriceProvider
}

var _ usecase.PriceProvider = (*Client)(nil)
