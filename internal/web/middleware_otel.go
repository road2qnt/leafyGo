package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"leafygo/internal/telemetry"
)

type requestInfoKey struct{}

// requestInfo is shared between the telemetry middleware and the inner auth/db
// middleware on the same request. The request is handled by one goroutine, so a
// plain struct is sufficient.
type requestInfo struct {
	Actor  string
	DBID   string
	DBName string
}

func withRequestInfo(ctx context.Context, info *requestInfo) context.Context {
	return context.WithValue(ctx, requestInfoKey{}, info)
}

func requestInfoFrom(ctx context.Context) *requestInfo {
	info, _ := ctx.Value(requestInfoKey{}).(*requestInfo)
	return info
}

// telemetryMiddleware starts one HTTP span, propagates W3C trace context,
// assigns a correlation id, and emits the structured http.request event.
func telemetryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipTelemetryPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			correlationID := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
			if correlationID == "" {
				correlationID = telemetry.NewCorrelationID()
			}
			ctx := telemetry.WithCorrelationID(r.Context(), correlationID)
			ctx = telemetry.ExtractTraceContext(ctx, r.Header.Get("traceparent"), r.Header.Get("tracestate"))

			spanName := "HTTP " + r.Method + " " + r.URL.Path
			ctx, span := telemetry.StartSpan(ctx, spanName,
				attribute.String("http.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			)
			info := &requestInfo{}
			ctx = withRequestInfo(ctx, info)

			if traceID := telemetry.TraceIDFromContext(ctx); traceID != "" {
				w.Header().Set("X-Trace-ID", traceID)
			}
			w.Header().Set("X-Correlation-ID", correlationID)

			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			defer func() {
				route := ""
				if routeCtx := chi.RouteContext(ctx); routeCtx != nil {
					route = routeCtx.RoutePattern()
				}
				span.SetAttributes(
					attribute.String("http.route", route),
					attribute.Int("http.status_code", recorder.status),
				)
				if route != "" {
					span.SetName("HTTP " + r.Method + " " + route)
				}
				if recorder.status >= http.StatusInternalServerError {
					span.SetStatus(codes.Error, http.StatusText(recorder.status))
				}

				attrs := []any{
					"method", r.Method,
					"path", r.URL.Path,
					"route", route,
					"status", recorder.status,
					"duration_ms", time.Since(start).Milliseconds(),
				}
				if info.Actor != "" {
					attrs = append(attrs, "actor", info.Actor)
				}
				if info.DBID != "" || info.DBName != "" {
					attrs = append(attrs, "db_id", info.DBID, "db_name", info.DBName)
					database := info.DBName
					if database == "" {
						database = info.DBID
					}
					attrs = append(attrs, "database", database)
					span.SetAttributes(
						attribute.String("db_id", info.DBID),
						attribute.String("db_name", info.DBName),
					)
				}
				if info.Actor != "" {
					span.SetAttributes(attribute.String("actor", info.Actor))
				}
				logger.InfoContext(ctx, "http.request", attrs...)
				span.End()
			}()

			next.ServeHTTP(recorder, r.WithContext(ctx))
		})
	}
}

func skipTelemetryPath(path string) bool {
	switch {
	case path == "/metrics", path == "/favicon.ico":
		return true
	case strings.HasPrefix(path, "/static/"):
		return true
	case strings.HasPrefix(path, "/app/assets/"):
		return true
	default:
		return false
	}
}
