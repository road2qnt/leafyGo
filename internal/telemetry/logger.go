// Package telemetry provides structured activity logging, per-source SQL
// privacy, OpenTelemetry trace propagation, and Prometheus metrics for LeafyGo.
package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
)

// MaxLoggedQueryBytes caps the query text written to structured logs. The full
// SQL is still available in migration_executions.executed_sql and its SHA-256
// digest is always logged.
const MaxLoggedQueryBytes = 2048

const truncatedQuerySuffix = "...[TRUNCATED]"

// SQLSource identifies where a SQL statement originated. The value drives the
// parameter-summary privacy policy.
type SQLSource string

const (
	SQLSourcePlatform     SQLSource = "platform"
	SQLSourceTargetViewer SQLSource = "target_viewer"
	SQLSourceQueryConsole SQLSource = "query_console"
	SQLSourceDryRun       SQLSource = "dry_run"
	SQLSourceMigration    SQLSource = "migration"
)

// LoggerOptions configures the JSON activity logger.
type LoggerOptions struct {
	Writer     io.Writer
	Level      slog.Level
	Service    string
	Env        string
	Version    string
	AddSource  bool
	TimeFormat string
}

// SQLMeta carries the identifiers and privacy policy inputs for one SQL
// statement. Callers must pass the real query; this package handles redaction,
// hashing, and truncation.
type SQLMeta struct {
	Source SQLSource
	Query  string
	Params []any

	Actor  string
	DBID   string
	DBName string

	MigrationID string
	JobID       string
	ExecutionID string
	Attempt     int
	BatchNo     int

	CursorFrom *int64
	CursorTo   *int64
	BatchSize  *int
}

// NewLogger builds a one-line-per-event JSON logger. Every record gets the
// current trace_id/span_id/correlation_id plus actor/database from context, and
// sensitive keys and literals are scrubbed before output.
func NewLogger(opts LoggerOptions) *slog.Logger {
	if opts.Writer == nil {
		opts.Writer = os.Stdout
	}
	level := opts.Level
	if level == 0 {
		level = slog.LevelInfo
	}
	jsonHandler := slog.NewJSONHandler(opts.Writer, &slog.HandlerOptions{
		Level:       level,
		AddSource:   opts.AddSource,
		ReplaceAttr: replaceAttr,
	})
	handler := &contextHandler{next: jsonHandler}
	logger := slog.New(handler)
	if opts.Service != "" {
		logger = logger.With("service", opts.Service)
	}
	if opts.Env != "" {
		logger = logger.With("env", opts.Env)
	}
	if opts.Version != "" {
		logger = logger.With("version", opts.Version)
	}
	return logger
}

var defaultLogger atomic.Pointer[slog.Logger]

func init() {
	defaultLogger.Store(NewLogger(LoggerOptions{Service: "leafygo"}))
}

// SetDefaultLogger makes the package-level SQL helpers use logger.
func SetDefaultLogger(logger *slog.Logger) {
	if logger == nil {
		return
	}
	defaultLogger.Store(logger)
}

// DefaultLogger returns the logger used by the package-level helpers.
func DefaultLogger() *slog.Logger {
	logger := defaultLogger.Load()
	if logger == nil {
		return slog.Default()
	}
	return logger
}

// NewCorrelationID returns a short request correlation identifier.
func NewCorrelationID() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(buf[:])
}

type ctxKey int

const (
	correlationKey ctxKey = iota
	actorKey
	dbIDKey
	dbNameKey
)

// WithCorrelationID attaches a correlation identifier to ctx.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey, id)
}

// CorrelationFrom returns the correlation identifier stored in ctx.
func CorrelationFrom(ctx context.Context) string {
	v, _ := ctx.Value(correlationKey).(string)
	return v
}

// WithActor attaches the acting user or worker identity to ctx.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey, actor)
}

// ActorFrom returns the actor stored in ctx.
func ActorFrom(ctx context.Context) string {
	v, _ := ctx.Value(actorKey).(string)
	return v
}

// WithDatabase attaches target database identity to ctx.
func WithDatabase(ctx context.Context, dbID, dbName string) context.Context {
	ctx = context.WithValue(ctx, dbIDKey, dbID)
	return context.WithValue(ctx, dbNameKey, dbName)
}

// DatabaseFrom returns the database identity stored in ctx.
func DatabaseFrom(ctx context.Context) (string, string) {
	id, _ := ctx.Value(dbIDKey).(string)
	name, _ := ctx.Value(dbNameKey).(string)
	return id, name
}

// contextHandler injects trace and request identity into every record without
// requiring callers to repeat them.
type contextHandler struct {
	next slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	present := make(map[string]bool, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		present[attr.Key] = true
		return true
	})

	var extra []slog.Attr
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		if !present["trace_id"] {
			extra = append(extra, slog.String("trace_id", sc.TraceID().String()))
		}
		if !present["span_id"] {
			extra = append(extra, slog.String("span_id", sc.SpanID().String()))
		}
	}
	if correlationID := CorrelationFrom(ctx); correlationID != "" && !present["correlation_id"] {
		extra = append(extra, slog.String("correlation_id", correlationID))
	}
	if actor := ActorFrom(ctx); actor != "" {
		if !present["actor"] {
			extra = append(extra, slog.String("actor", actor))
		}
	}
	if dbID, dbName := DatabaseFrom(ctx); dbID != "" || dbName != "" {
		if !present["db_id"] && dbID != "" {
			extra = append(extra, slog.String("db_id", dbID))
		}
		if !present["db_name"] && dbName != "" {
			extra = append(extra, slog.String("db_name", dbName))
		}
		if !present["database"] {
			database := dbName
			if database == "" {
				database = dbID
			}
			extra = append(extra, slog.String("database", database))
		}
	}
	if len(extra) > 0 {
		record.AddAttrs(extra...)
	}
	return h.next.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}

func replaceAttr(groups []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.MessageKey {
		attr.Key = "event"
		return attr
	}
	if attr.Key == "" {
		// Group boundary; values are visited individually.
		return attr
	}
	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, "[REDACTED]")
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, Sanitize(attr.Value.String()))
	case slog.KindAny:
		switch value := attr.Value.Any().(type) {
		case error:
			return slog.String(attr.Key, Sanitize(value.Error()))
		case []byte:
			return slog.String(attr.Key, "[REDACTED]")
		}
	}
	return attr
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "password", "passwd", "pwd", "password_hash", "password_enc",
		"secret", "token", "access_token", "refresh_token", "csrf", "csrf_token",
		"sid", "session", "session_id", "authorization", "cookie", "set-cookie",
		"api_key", "apikey", "dsn", "database_url", "encryption_key", "private_key":
		return true
	}
	return strings.HasSuffix(normalized, "_password") ||
		strings.HasSuffix(normalized, "_secret") ||
		strings.HasSuffix(normalized, "_token") ||
		strings.HasSuffix(normalized, "_key")
}

var (
	sqlPasswordLiteral = regexp.MustCompile(`(?i)\b(ENCRYPTED\s+PASSWORD|PASSWORD(?:_HASH|_ENC)?|SECRET|TOKEN|API_KEY)(\s*(?:=|:|\s)\s*)(E?)'((?:[^']|'')*)'`)
	sqlDollarLiteral   = regexp.MustCompile(`(?i)\b(ENCRYPTED\s+PASSWORD|PASSWORD(?:_HASH|_ENC)?|SECRET|TOKEN|API_KEY)(\s*(?:=|:|\s)\s*)\$[A-Za-z0-9_]*\$[^$]*\$[A-Za-z0-9_]*\$`)
	sqlPGPSymmetric    = regexp.MustCompile(`(?i)(pgp_sym_(?:encrypt|decrypt)\s*\([^,]+,\s*)'((?:[^']|'')*)'`)
	dsnPassword        = regexp.MustCompile(`(?i)\b(postgres(?:ql)?://[^:/@\s]+:)([^@\s]+)(@)`)
	keyValuePassword   = regexp.MustCompile(`(?i)\b(password(?:_hash|_enc)?|passwd|pwd|secret|token|api_key)\s*=\s*([^\s;']+)`)
	pgKeyDetail        = regexp.MustCompile(`(?i)\bKey\s*\(([^)]*)\)\s*=\s*\(([^)]*)\)`)
	pgFailingRow       = regexp.MustCompile(`(?i)Failing row contains\s*\([^)]*\)`)
)

// Sanitize redacts credential literals and target-row detail from SQL text,
// DSNs, key/value pairs, and PostgreSQL error details.
func Sanitize(text string) string {
	if text == "" {
		return text
	}
	text = sqlDollarLiteral.ReplaceAllString(text, `${1}${2}$[REDACTED]$`)
	text = sqlPasswordLiteral.ReplaceAllString(text, `${1}${2}${3}'[REDACTED]'`)
	text = sqlPGPSymmetric.ReplaceAllString(text, `${1}'[REDACTED]'`)
	text = dsnPassword.ReplaceAllString(text, `${1}[REDACTED]${3}`)
	text = keyValuePassword.ReplaceAllString(text, `${1}=[REDACTED]`)
	text = pgKeyDetail.ReplaceAllString(text, `Key ($1)=([REDACTED])`)
	text = pgFailingRow.ReplaceAllString(text, `Failing row contains ([REDACTED])`)
	return text
}

// TruncateQuery caps query text at MaxLoggedQueryBytes while keeping valid UTF-8.
func TruncateQuery(query string) string {
	if len(query) <= MaxLoggedQueryBytes {
		return query
	}
	limit := MaxLoggedQueryBytes - len(truncatedQuerySuffix)
	if limit < 0 {
		limit = 0
	}
	for limit > 0 && !utf8.RuneStart(query[limit]) {
		limit--
	}
	return query[:limit] + truncatedQuerySuffix
}

// HashQuery returns the hex SHA-256 of the full query text.
func HashQuery(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:])
}

func paramCount(meta SQLMeta) int {
	return len(meta.Params)
}

func paramSummary(meta SQLMeta) any {
	switch meta.Source {
	case SQLSourceTargetViewer, SQLSourceQueryConsole:
		hashes := make([]string, 0, len(meta.Params))
		for _, param := range meta.Params {
			sum := sha256.Sum256([]byte(canonicalParam(param)))
			hashes = append(hashes, "sha256:"+hex.EncodeToString(sum[:8]))
		}
		return hashes
	case SQLSourceMigration, SQLSourceDryRun:
		summary := make(map[string]any, 3)
		if meta.CursorFrom != nil {
			summary["cursor_from"] = *meta.CursorFrom
		}
		if meta.CursorTo != nil {
			summary["cursor_to"] = *meta.CursorTo
		}
		if meta.BatchSize != nil {
			summary["batch_size"] = *meta.BatchSize
		}
		return summary
	default:
		// Platform SQL never records raw bind values: they may contain
		// password_hash, password_enc, session ids, or CSRF tokens.
		placeholders := make([]string, 0, len(meta.Params))
		for i := range meta.Params {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
		}
		return placeholders
	}
}

func canonicalParam(param any) string {
	switch value := param.(type) {
	case nil:
		return "<nil>"
	case string:
		return value
	case []byte:
		return hex.EncodeToString(value)
	case time.Time:
		return value.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func baseSQLAttrs(meta SQLMeta) []any {
	attrs := make([]any, 0, 24)
	attrs = append(attrs,
		"sql_source", string(meta.Source),
		"query", TruncateQuery(Sanitize(meta.Query)),
		"query_sha256", HashQuery(meta.Query),
		"param_count", paramCount(meta),
		"params_summary", paramSummary(meta),
	)
	if meta.Actor != "" {
		attrs = append(attrs, "actor", meta.Actor)
	}
	if meta.DBID != "" {
		attrs = append(attrs, "db_id", meta.DBID)
	}
	if meta.DBName != "" {
		attrs = append(attrs, "db_name", meta.DBName, "database", meta.DBName)
	}
	if meta.MigrationID != "" {
		attrs = append(attrs, "migration_id", meta.MigrationID)
	}
	if meta.JobID != "" {
		attrs = append(attrs, "job_id", meta.JobID)
	}
	if meta.ExecutionID != "" {
		attrs = append(attrs, "execution_id", meta.ExecutionID)
	}
	if meta.Attempt > 0 {
		attrs = append(attrs, "attempt", meta.Attempt)
	}
	if meta.BatchNo > 0 {
		attrs = append(attrs, "batch_no", meta.BatchNo)
	}
	return attrs
}

// LogSQLStart emits the sql.exec.start event.
func LogSQLStart(ctx context.Context, meta SQLMeta) {
	LogSQLStartWith(DefaultLogger(), ctx, meta)
}

// LogSQLStartWith emits sql.exec.start through an explicit logger.
func LogSQLStartWith(logger *slog.Logger, ctx context.Context, meta SQLMeta) {
	if logger == nil {
		logger = DefaultLogger()
	}
	attrs := baseSQLAttrs(meta)
	attrs = append(attrs, "status", "started")
	logger.InfoContext(ctx, "sql.exec.start", attrs...)
}

// LogSQLResult emits sql.exec.result with duration, row count, and SQLSTATE.
func LogSQLResult(ctx context.Context, meta SQLMeta, rowsAffected int64, duration time.Duration, err error) {
	LogSQLResultWith(DefaultLogger(), ctx, meta, rowsAffected, duration, err)
}

// LogSQLResultWith emits sql.exec.result through an explicit logger.
func LogSQLResultWith(logger *slog.Logger, ctx context.Context, meta SQLMeta, rowsAffected int64, duration time.Duration, err error) {
	if logger == nil {
		logger = DefaultLogger()
	}
	attrs := baseSQLAttrs(meta)
	attrs = append(attrs,
		"status", statusForError(err),
		"rows_affected", rowsAffected,
		"row_count", rowsAffected,
		"duration_ms", duration.Milliseconds(),
	)
	if err != nil {
		attrs = append(attrs, "error", Sanitize(err.Error()))
		if state := sqlState(err); state != "" {
			attrs = append(attrs, "sqlstate", state)
		}
		logger.ErrorContext(ctx, "sql.exec.result", attrs...)
		return
	}
	logger.InfoContext(ctx, "sql.exec.result", attrs...)
}

// LogSQLTxEnd emits sql.tx.commit or sql.tx.rollback.
func LogSQLTxEnd(ctx context.Context, meta SQLMeta, outcome string, duration time.Duration, err error) {
	LogSQLTxEndWith(DefaultLogger(), ctx, meta, outcome, duration, err)
}

// LogSQLTxEndWith emits the transaction boundary event through an explicit
// logger. Unknown outcomes are treated as rollback, the safe default.
func LogSQLTxEndWith(logger *slog.Logger, ctx context.Context, meta SQLMeta, outcome string, duration time.Duration, err error) {
	if logger == nil {
		logger = DefaultLogger()
	}
	normalized := strings.ToLower(strings.TrimSpace(outcome))
	if normalized != "commit" {
		normalized = "rollback"
	}
	attrs := baseSQLAttrs(meta)
	attrs = append(attrs, "status", normalized, "duration_ms", duration.Milliseconds())
	if err != nil {
		attrs = append(attrs, "error", Sanitize(err.Error()))
		if state := sqlState(err); state != "" {
			attrs = append(attrs, "sqlstate", state)
		}
	}
	event := "sql.tx." + normalized
	if err != nil {
		logger.ErrorContext(ctx, event, attrs...)
		return
	}
	logger.InfoContext(ctx, event, attrs...)
}

func statusForError(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}
