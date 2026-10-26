package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Логи процесса (docs/observability.md#логи): одна JSON-запись на строку в
// stdout. Поля timestamp (UTC), severity и message, resource-поля service.* и
// deployment.environment.name из того же resource, что у traces и metrics,
// trace_id/span_id — только внутри span. Значения проходят redaction до записи.

// EnvLogLevel — минимальный уровень записей: debug, info (по умолчанию), warn, error.
const EnvLogLevel = "LOG__LEVEL"

// Ключи записи. Resource-поля — плоские ключи с точками, как их читает Alloy.
const (
	LogTimestampKey = "timestamp"
	LogSeverityKey  = "severity"
	LogMessageKey   = "message"
	LogTraceIDKey   = "trace_id"
	LogSpanIDKey    = "span_id"
	// LogErrorKey — соглашение для ошибок: slog.Any(LogErrorKey, err) пишется
	// полями error.message (после redaction) и error.type.
	LogErrorKey        = "error"
	LogErrorMessageKey = "error.message"
	LogErrorTypeKey    = "error.type"
)

// error.type отменённого вызова и истёкшего deadline.
const (
	ErrorTypeCanceled         = "canceled"
	ErrorTypeDeadlineExceeded = "deadline_exceeded"
)

// Redacted заменяет значение, которое не должно попасть в telemetry.
const Redacted = "[REDACTED]"

// LogResource — идентификация процесса в каждой записи.
type LogResource struct {
	Namespace   string
	Name        string
	Version     string
	Environment string
	InstanceID  string
}

// LogResourceFor читает тот же resource, что Start: serviceName по умолчанию,
// OTEL_SERVICE_NAME и OTEL_RESOURCE_ATTRIBUTES его переопределяют.
func LogResourceFor(ctx context.Context, serviceName string) (LogResource, error) {
	res, err := newResource(ctx, serviceName)
	if err != nil {
		return LogResource{Namespace: ServiceNamespace, Name: serviceName, Version: buildVersion()}, err
	}
	lr := LogResource{}
	for _, kv := range res.Attributes() {
		switch kv.Key {
		case semconv.ServiceNamespaceKey:
			lr.Namespace = kv.Value.AsString()
		case semconv.ServiceNameKey:
			lr.Name = kv.Value.AsString()
		case semconv.ServiceVersionKey:
			lr.Version = kv.Value.AsString()
		case semconv.DeploymentEnvironmentNameKey:
			lr.Environment = kv.Value.AsString()
		case semconv.ServiceInstanceIDKey:
			lr.InstanceID = kv.Value.AsString()
		}
	}
	return lr, nil
}

func (r LogResource) attrs() []slog.Attr {
	attrs := make([]slog.Attr, 0, 5)
	for _, kv := range []struct {
		key   attribute.Key
		value string
	}{
		{semconv.ServiceNamespaceKey, r.Namespace},
		{semconv.ServiceNameKey, r.Name},
		{semconv.ServiceVersionKey, r.Version},
		{semconv.DeploymentEnvironmentNameKey, r.Environment},
		{semconv.ServiceInstanceIDKey, r.InstanceID},
	} {
		// Неизвестное значение не подменяется фиктивным: поля просто нет.
		if kv.value != "" {
			attrs = append(attrs, slog.String(string(kv.key), kv.value))
		}
	}
	return attrs
}

// NewLogHandler — JSON handler по схеме логов с resource-полями и trace context.
func NewLogHandler(w io.Writer, resource LogResource, level slog.Leveler) slog.Handler {
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: replaceAttr})
	return traceHandler{inner: inner.WithAttrs(resource.attrs())}
}

// SetupLogging делает JSON logger процесса default logger'ом: через него идут
// slog.*, log.Printf (slog.SetDefault перенаправляет пакет log) и gRPC
// (grpclog).
// Вызывается первым в main, до загрузки конфигурации.
func SetupLogging(serviceName string) *slog.Logger {
	resource, resErr := LogResourceFor(context.Background(), serviceName)
	level, levelErr := ParseLogLevel(os.Getenv(EnvLogLevel))
	logger := slog.New(NewLogHandler(os.Stdout, resource, level))
	slog.SetDefault(logger)
	setGRPCLogger(logger)
	if resErr != nil {
		logger.Warn("telemetry resource is incomplete", LogErrorKey, resErr)
	}
	if levelErr != nil {
		logger.Warn("log level is invalid, using info", LogErrorKey, levelErr)
	}
	return logger
}

// ParseLogLevel разбирает LOG__LEVEL; пустое значение — info.
func ParseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("%s must be debug, info, warn or error, got %q", EnvLogLevel, raw)
	}
}

// traceHandler добавляет trace_id и span_id активного span из context записи.
// Вне span полей нет. Поля добавляются к записи, поэтому внутри WithGroup они
// окажутся в группе; код controlplane группы не открывает.
type traceHandler struct{ inner slog.Handler }

func (h traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h traceHandler) Handle(ctx context.Context, record slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		record = record.Clone()
		record.AddAttrs(slog.String(LogTraceIDKey, sc.TraceID().String()), slog.String(LogSpanIDKey, sc.SpanID().String()))
	}
	return h.inner.Handle(ctx, record)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{inner: h.inner.WithGroup(name)}
}

// replaceAttr переименовывает встроенные поля по схеме и применяет redaction.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			return slog.String(LogTimestampKey, a.Value.Time().UTC().Format(time.RFC3339Nano))
		case slog.LevelKey:
			level, _ := a.Value.Any().(slog.Level)
			return slog.String(LogSeverityKey, severity(level))
		case slog.MessageKey:
			return slog.String(LogMessageKey, Scrub(a.Value.String()))
		}
	}
	if sensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Scrub(a.Value.String()))
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			if a.Key == LogErrorKey {
				// Пустой ключ группы встраивает поля в запись: error.message, error.type.
				return slog.Attr{Value: slog.GroupValue(errorAttrs(v)...)}
			}
			return slog.String(a.Key, Scrub(v.Error()))
		case []byte:
			// Байты — payload сообщений и запросов; в логи не попадают.
			return slog.String(a.Key, Redacted)
		case fmt.Stringer:
			return slog.String(a.Key, Scrub(v.String()))
		}
	}
	return a
}

// severity — нормализованный уровень (severity_text OpenTelemetry).
func severity(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}

// sensitiveKeys — denylist имён полей: значение заменяется целиком.
var sensitiveKeys = []string{
	"token", "password", "passwd", "secret", "dsn", "authorization", "cookie",
	"credential", "preshared", "api_key", "apikey", "private_key", "payload",
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(key, s) {
			return true
		}
	}
	return false
}

var scrubbers = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// Userinfo в URL и DSN: postgres://user:password@host → postgres://[REDACTED]@host.
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]+@`), "${1}" + Redacted + "@"},
	// key=value DSN и query: password=…, sslpassword=…, token=…, secret=….
	{regexp.MustCompile(`(?i)\b(password|passwd|pwd|sslpassword|token|access_token|refresh_token|secret|preshared_key)=[^\s&]+`), "${1}=" + Redacted},
	// Authorization header.
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]+`), "${1} " + Redacted},
	// JWT (header.payload.signature) в любом тексте.
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`), Redacted},
}

// Scrub удаляет из текста credentials, DSN userinfo, bearer tokens и JWT.
// Применяется ко всем строковым полям и message; ошибки внешних библиотек могут
// содержать запрос целиком.
func Scrub(s string) string {
	for _, sc := range scrubbers {
		s = sc.pattern.ReplaceAllString(s, sc.replacement)
	}
	return s
}

func errorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{slog.String(LogErrorMessageKey, Scrub(err.Error()))}
	if t := ErrorType(err); t != "" {
		attrs = append(attrs, slog.String(LogErrorTypeKey, t))
	}
	return attrs
}

// ErrorType — низкокардинальный тип ошибки для поля error.type: отмена,
// gRPC code, SQLSTATE PostgreSQL или Go-тип самой внутренней ошибки; для
// безымянных ошибок (errors.New, fmt.Errorf) — _OTHER, как в semconv error.type.
func ErrorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return ErrorTypeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorTypeDeadlineExceeded
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		return "sqlstate_" + sqlState.SQLState()
	}
	if s, ok := status.FromError(err); ok && s.Code() != codes.OK {
		return "grpc_" + s.Code().String()
	}
	inner := err
	for {
		next := errors.Unwrap(inner)
		if next == nil {
			break
		}
		inner = next
	}
	switch name := fmt.Sprintf("%T", inner); name {
	case "*errors.errorString", "*fmt.wrapError", "*fmt.wrapErrors", "*errors.joinError":
		return "_OTHER"
	default:
		return name
	}
}

// buildVersion — service.version по умолчанию: VCS revision сборки; env
// (OTEL_RESOURCE_ATTRIBUTES service.version, его задаёт Helm) переопределяет.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	switch {
	case revision != "" && modified == "true":
		return revision + "-dirty"
	case revision != "":
		return revision
	case info.Main.Version != "" && info.Main.Version != "(devel)":
		return info.Main.Version
	default:
		return "unknown"
	}
}
