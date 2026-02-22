package app

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/http/handlers"
	"github.com/sanchey92/order-processor/internal/http/middlewares"
	"github.com/sanchey92/order-processor/internal/service/order"
)

func initHTTPServer(cfg config.HTTP, orderService *order.Service, logger *slog.Logger) *http.Server {
	r := chi.NewRouter()
	r.Use(middlewares.Recovery(logger))
	r.Use(middleware.RequestID)

	r.Route("/api/v1/orders", func(r chi.Router) {
		r.Get("/{id}", handlers.GetByID(orderService))
		r.Post("/", handlers.Create(orderService))
	})

	return &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}
}
