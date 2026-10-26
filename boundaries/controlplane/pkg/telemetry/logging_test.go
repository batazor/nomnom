package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/telemetry"
)

var testResource = telemetry.LogResource{
	Namespace: "nomnom", Name: "controlplane", Version: "1.2.3", Environment: "test", InstanceID: "controlplane-0",
}

func logRecords(t *testing.T, write func(*slog.Logger)) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	write(slog.New(telemetry.NewLogHandler(&out, testResource, slog.LevelDebug)))
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		record := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(line), &record), "one JSON object per line: %s", line)
		records = append(records, record)
	}
	return records
}

// Every record carries the schema fields of docs/observability.md#логи.
func TestLogRecordFollowsSchema(t *testing.T) {
	records := logRecords(t, func(l *slog.Logger) { l.Warn("order placed", "order_id", "o-1") })
	require.Len(t, records, 1)
	r := records[0]

	assert.Equal(t, "WARN", r["severity"])
	assert.Equal(t, "order placed", r["message"])
	assert.Equal(t, "o-1", r["order_id"])
	for key, want := range map[string]string{
		"service.namespace": "nomnom", "service.name": "controlplane", "service.version": "1.2.3",
		"deployment.environment.name": "test", "service.instance.id": "controlplane-0",
	} {
		assert.Equal(t, want, r[key], key)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, r["timestamp"].(string))
	require.NoError(t, err)
	assert.Equal(t, time.UTC, timestamp.Location())
	assert.True(t, strings.HasSuffix(r["timestamp"].(string), "Z"))
	for _, builtin := range []string{"time", "level", "msg"} {
		assert.NotContains(t, r, builtin)
	}
	assert.NotContains(t, r, "trace_id", "no span, no trace_id")
	assert.NotContains(t, r, "span_id")
}

func TestLogSeverityIsNormalized(t *testing.T) {
	records := logRecords(t, func(l *slog.Logger) {
		l.Debug("d")
		l.Info("i")
		l.Log(context.Background(), slog.LevelWarn+2, "w")
		l.Error("e")
	})
	var got []any
	for _, r := range records {
		got = append(got, r["severity"])
	}
	assert.Equal(t, []any{"DEBUG", "INFO", "WARN", "ERROR"}, got)
}

// trace_id and span_id come from the span in context, and only inside one.
func TestLogRecordCarriesTraceContextInsideSpan(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, span := provider.Tracer("test").Start(context.Background(), "usecase place_order")
	defer span.End()

	records := logRecords(t, func(l *slog.Logger) {
		l.InfoContext(ctx, "inside")
		l.InfoContext(context.Background(), "outside")
		l.With("component", "x").InfoContext(ctx, "derived logger")
	})
	require.Len(t, records, 3)
	assert.Equal(t, span.SpanContext().TraceID().String(), records[0]["trace_id"])
	assert.Equal(t, span.SpanContext().SpanID().String(), records[0]["span_id"])
	assert.NotContains(t, records[1], "trace_id")
	assert.NotContains(t, records[1], "span_id")
	assert.Equal(t, span.SpanContext().TraceID().String(), records[2]["trace_id"])
}

// Errors become error.message and error.type; their text is scrubbed.
func TestLogErrorFields(t *testing.T) {
	records := logRecords(t, func(l *slog.Logger) {
		l.Error("connect failed", telemetry.LogErrorKey,
			fmt.Errorf("dial postgres://controlplane:s3cret@db:5432/controlplane: %w", context.DeadlineExceeded))
		l.Error("rpc failed", telemetry.LogErrorKey, fmt.Errorf("catalog: %w", status.Error(codes.Unavailable, "down")))
		l.Error("plain", telemetry.LogErrorKey, errors.New("boom"))
	})
	require.Len(t, records, 3)
	assert.Equal(t, "dial postgres://[REDACTED]@db:5432/controlplane: context deadline exceeded", records[0]["error.message"])
	assert.Equal(t, "deadline_exceeded", records[0]["error.type"])
	assert.NotContains(t, records[0], "error")
	assert.Equal(t, "grpc_Unavailable", records[1]["error.type"])
	assert.Equal(t, "_OTHER", records[2]["error.type"])
}

// Redaction: secrets by field name and inside any text, payload bytes never.
func TestLogRedaction(t *testing.T) {
	const jwt = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyLTEifQ.c2lnbmF0dXJl"
	records := logRecords(t, func(l *slog.Logger) {
		l.Info("calling with Bearer "+jwt,
			"access_token", "abc",
			"POSTGRES__DSN", "host=db user=controlplane password=s3cret",
			"preshared_key", "devkey",
			"Authorization", "Basic dXNlcjpwYXNz",
			"payload", "{\"card\":\"4111\"}",
			"body", []byte("raw message"),
			"detail", "dsn host=db password=s3cret sslmode=disable",
			"url", "https://user:pass@example.com/path?token=xyz&page=2",
			"assertion", jwt,
			"order_id", "o-1",
		)
	})
	require.Len(t, records, 1)
	r := records[0]
	line, err := json.Marshal(r)
	require.NoError(t, err)
	for _, secret := range []string{"abc", "s3cret", "devkey", "dXNlcjpwYXNz", "4111", "raw message", "user:pass", "xyz", jwt} {
		assert.NotContains(t, string(line), secret)
	}
	for _, key := range []string{"access_token", "POSTGRES__DSN", "preshared_key", "Authorization", "payload", "body"} {
		assert.Equal(t, telemetry.Redacted, r[key], key)
	}
	assert.Equal(t, "calling with Bearer [REDACTED]", r["message"])
	assert.Equal(t, "dsn host=db password=[REDACTED] sslmode=disable", r["detail"])
	assert.Equal(t, "https://[REDACTED]@example.com/path?token=[REDACTED]&page=2", r["url"])
	assert.Equal(t, telemetry.Redacted, r["assertion"])
	assert.Equal(t, "o-1", r["order_id"], "allowed lookup identifiers stay")
}

// Resource fields follow OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES, as for traces and metrics.
func TestLogResourceFollowsOTelEnv(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "controlplane-consumer")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=local,service.version=abc123,service.instance.id=pod-1")
	resource, err := telemetry.LogResourceFor(context.Background(), "controlplane")
	require.NoError(t, err)
	assert.Equal(t, telemetry.LogResource{
		Namespace: "nomnom", Name: "controlplane-consumer", Version: "abc123", Environment: "local", InstanceID: "pod-1",
	}, resource)

	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	resource, err = telemetry.LogResourceFor(context.Background(), "controlplane-reconciler")
	require.NoError(t, err)
	assert.Equal(t, "controlplane-reconciler", resource.Name)
	assert.NotEmpty(t, resource.Version, "build version by default")
	assert.Empty(t, resource.Environment, "unknown environment is absent, not faked")
}

func TestParseLogLevel(t *testing.T) {
	for raw, want := range map[string]slog.Level{"": slog.LevelInfo, "debug": slog.LevelDebug, "WARN": slog.LevelWarn, "error": slog.LevelError} {
		got, err := telemetry.ParseLogLevel(raw)
		require.NoError(t, err)
		assert.Equal(t, want, got, raw)
	}
	_, err := telemetry.ParseLogLevel("verbose")
	assert.Error(t, err)
}
