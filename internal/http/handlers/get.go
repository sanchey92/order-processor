package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	er "github.com/sanchey92/order-processor/internal/domain/errors"
	"github.com/sanchey92/order-processor/internal/domain/model"
	"github.com/sanchey92/order-processor/internal/http/lib/api/response"
)

type OrderGetter interface {
	GetOrder(ctx context.Context, id string) (*model.Order, error)
}

type getResponse struct {
	ID     string            `json:"id"`
	UserID string            `json:"user_id"`
	Items  []model.OrderItem `json:"items"`
	Amount int64             `json:"amount"`
	Status model.OrderStatus `json:"status"`
}

func GetByID(orderGetter OrderGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if id == "" {
			response.BadRequest(w, "id is required")
			return
		}
		order, err := orderGetter.GetOrder(r.Context(), id)
		if err != nil {
			if errors.Is(err, er.ErrOrderNotFound) {
				response.NotFound(w, er.ErrOrderNotFound.Error())
				return
			}
			response.InternalError(w)
			return
		}
		response.OK(w, getResponse{
			ID:     order.ID,
			UserID: order.UserID,
			Items:  order.Items,
			Amount: order.Amount,
			Status: order.Status,
		})
	}
}
