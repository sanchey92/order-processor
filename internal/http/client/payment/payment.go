package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type ClientError struct {
	StatusCode int
	Body       string
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("payment: %d: %s", e.StatusCode, e.Body)
}

func IsServerFailure(err error) bool {
	var ce *ClientError
	return !errors.As(err, &ce)
}

type ReserveResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
}

type Breaker interface {
	Execute(ctx context.Context, fn func(context.Context) error) error
}

type Client struct {
	client  *http.Client
	baseURL string
	cb      Breaker
}

func New(baseURL string, timeout time.Duration, cb Breaker) *Client {
	return &Client{
		client:  &http.Client{Timeout: timeout},
		baseURL: baseURL,
		cb:      cb,
	}
}

func (c *Client) Reserve(ctx context.Context, orderID string, amount int64) (*ReserveResponse, error) {
	var result *ReserveResponse

	err := c.cb.Execute(ctx, func(ctx context.Context) error {
		body, err := json.Marshal(map[string]any{"order_id": orderID, "amount_cents": amount})
		if err != nil {
			return fmt.Errorf("reserve.marshal_body: %w", err)
		}
		url := fmt.Sprintf("%s/api/v1/payment/reserve", c.baseURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("NewRequestWithContext: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}

		if resp.StatusCode >= 400 {
			return &ClientError{StatusCode: resp.StatusCode}
		}

		var r ReserveResponse
		if err = json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		result = &r
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("payment.Service: %w", err)
	}

	return result, nil
}

func (c *Client) Cancel(ctx context.Context, paymentID string) error {
	return c.cb.Execute(ctx, func(ctx context.Context) error {
		url := fmt.Sprintf("%s/api/v1/payment/%s/cancel", c.baseURL, paymentID)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			return fmt.Errorf("new cancel request: %w", err)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			return fmt.Errorf("cancel error: %d", resp.StatusCode)
		}
		return nil
	})
}
