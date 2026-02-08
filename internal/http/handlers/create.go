package handlers

import (
	"context"
	"net/http"

	"github.com/sanchey92/order-processor/internal/domain/model"
	"github.com/sanchey92/order-processor/internal/http/lib/api/decode"
	"github.com/sanchey92/order-processor/internal/http/lib/api/response"
)

type OrderService interface {
	Create(ctx context.Context, cmd *model.CreateOrderCommand) (*model.Order, error)
}

type createRequest struct {
	UserID string            `json:"user_id"`
	Items  []model.OrderItem `json:"items"`
}

type createResponse struct {
	OrderID string            `json:"order_id"`
	Status  model.OrderStatus `json:"status"`
	Message string            `json:"message"`
}

func Create(service OrderService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest

		if err := decode.JSON(r, &req); err != nil {
			response.BadRequest(w, err.Error())
			return
		}

		order, err := service.Create(r.Context(), &model.CreateOrderCommand{
			UserID: req.UserID,
			Items:  req.Items,
		})
		if err != nil {
			response.InternalError(w)
			return
		}

		response.Created(w, createResponse{
			OrderID: order.ID,
			Status:  order.Status,
			Message: "order created",
		})
	}
}
