package warehouse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

type ClientError struct {
	StatusCode int
}

func (ce *ClientError) Error() string {
	return fmt.Sprintf("warehouse: %d", ce.StatusCode)
}

func IsServerFailure(err error) bool {
	var ce *ClientError
	return !errors.As(err, &ce)
}

type ReserveResponse struct {
	ReservationID string `json:"reservation_id"`
}

type Breaker interface {
	Execute(ctx context.Context, fn func(context.Context) error) error
}

type Client struct {
	baseURL string
	client  *http.Client
	cb      Breaker
}

func New(baseURL string, timeout time.Duration, cb Breaker) *Client {
	return &Client{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
		cb:      cb,
	}
}

func (c *Client) Reserve(ctx context.Context, orderID string, items []model.OrderItem) (*ReserveResponse, error) {
	var result *ReserveResponse
	err := c.cb.Execute(ctx, func(ctx context.Context) error {
		body, err := json.Marshal(map[string]any{"order_id": orderID, "items": items})
		if err != nil {
			return fmt.Errorf("warehouse, failed to marshal: %w", err)
		}
		url := fmt.Sprintf("%s/api/v1/inventory/reserve", c.baseURL)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("warehouse, new request: %w", err)
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
		return nil, fmt.Errorf("warehouse.Reserve: %w", err)
	}

	return result, nil
}

func (c *Client) CancelReservation(ctx context.Context, reservationID string) error {
	return c.cb.Execute(ctx, func(ctx context.Context) error {
		url := fmt.Sprintf("%s/api/v1/inventory/%s/cancel", c.baseURL, reservationID)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			return fmt.Errorf("cancel reservation: %w", err)
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
