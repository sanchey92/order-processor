package model

import (
	"time"

	"github.com/google/uuid"
)

type OrderStatus string

const (
	OrderPending   OrderStatus = "PENDING"
	OrderConfirmed OrderStatus = "CONFIRMED"
	OrderCancelled OrderStatus = "CANCELLED"
)

type Order struct {
	ID        string      `json:"id"         db:"id"`
	UserID    string      `json:"user_id"    db:"user_id"`
	Items     []OrderItem `json:"items"      db:"-"`
	Amount    int64       `json:"amount"     db:"amount"` // cents
	Status    OrderStatus `json:"status"     db:"status"`
	CreatedAt time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt time.Time   `json:"updated_at" db:"updated_at"`
}

type OrderItem struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
	Price     int64  `json:"price"` // cents
}

func NewOrder(userID string, items []OrderItem) *Order {
	var total int64
	for _, item := range items {
		total += item.Price * int64(item.Quantity)
	}

	now := time.Now().UTC()
	return &Order{
		ID:        uuid.New().String(),
		UserID:    userID,
		Items:     items,
		Amount:    total,
		Status:    OrderPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type CreateOrderCommand struct {
	UserID string      `json:"user_id"`
	Items  []OrderItem `json:"items"`
}
