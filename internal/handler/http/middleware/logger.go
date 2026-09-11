package middleware

import (
	"log/slog"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// StructuredLogger returns a middleware that logs incoming HTTP requests using slog with status-aware log levels.
func StructuredLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

		defer func() {
			latency := time.Since(start)
			reqID := chimw.GetReqID(r.Context())

			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}

			args := []any{
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"bytes", ww.BytesWritten(),
				"duration_ms", float64(latency.Microseconds()) / 1000.0,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
			}

			switch {
			case status >= 500:
				slog.Error("http request error", args...)
			case status >= 400:
				slog.Warn("http request client warning", args...)
			default:
				slog.Info("http request", args...)
			}
		}()

		next.ServeHTTP(ww, r)
	})
}
