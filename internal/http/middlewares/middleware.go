package middlewares

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/sanchey92/order-processor/internal/http/lib/api/response"
)

func Recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic", slog.Any("panic", rec),
						slog.String("stack", string(debug.Stack())))
					response.InternalError(w)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
