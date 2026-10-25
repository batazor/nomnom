package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"nomnom/orders/pkg/telemetry"
)

var errRejected = errors.New("order not found")

func classify(err error) telemetry.Failure {
	if errors.Is(err, errRejected) {
		return telemetry.Failure{Outcome: telemetry.OutcomeRejected, ErrorType: "not_found"}
	}
	return telemetry.Failure{}
}

type providers struct {
	reader *sdkmetric.ManualReader
	spans  *tracetest.SpanRecorder
}

func install(t *testing.T) providers {
	t.Helper()
	p := providers{reader: sdkmetric.NewManualReader(), spans: tracetest.NewSpanRecorder()}
	previousMeter, previousTracer := otel.GetMeterProvider(), otel.GetTracerProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(p.reader)))
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(p.spans)))
	t.Cleanup(func() {
		otel.SetMeterProvider(previousMeter)
		otel.SetTracerProvider(previousTracer)
	})
	return p
}

func (p providers) durations(t *testing.T) []attribute.Set {
	t.Helper()
	var data metricdata.ResourceMetrics
	require.NoError(t, p.reader.Collect(context.Background(), &data))
	var got []attribute.Set
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "nomnom.usecase.duration" {
				continue
			}
			assert.Equal(t, "s", m.Unit)
			for _, point := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				assert.EqualValues(t, 1, point.Count)
				got = append(got, point.Attributes)
			}
		}
	}
	return got
}

func value(set attribute.Set, key attribute.Key) string {
	v, _ := set.Value(key)
	return v.AsString()
}

// Имя и attributes метрики — контракт ops/grafana/inventory (раздел usecase).
func TestInstrumentRecordsOutcomeOfEveryCall(t *testing.T) {
	p := install(t)
	uc := telemetry.NewUseCases(classify)
	handler := telemetry.Instrument(uc, telemetry.Command("place_order"), func(ctx context.Context, in string) (int, error) {
		switch in {
		case "rejected":
			return 0, errRejected
		case "broken":
			return 0, errors.New("database unavailable: password=secret")
		case "canceled":
			return 0, context.Canceled
		}
		return len(in), nil
	})

	out, err := handler.Handle(context.Background(), "ok")
	require.NoError(t, err)
	assert.Equal(t, 2, out)
	_, err = handler.Handle(context.Background(), "rejected")
	assert.ErrorIs(t, err, errRejected)
	_, err = handler.Handle(context.Background(), "broken")
	assert.EqualError(t, err, "database unavailable: password=secret")
	_, err = handler.Handle(context.Background(), "canceled")
	assert.ErrorIs(t, err, context.Canceled)

	got := map[string]string{}
	for _, attrs := range p.durations(t) {
		assert.Equal(t, "place_order", value(attrs, "nomnom.usecase.name"))
		assert.Equal(t, "command", value(attrs, "nomnom.usecase.kind"))
		got[value(attrs, "nomnom.outcome")+"/"+value(attrs, "error.type")] = ""
	}
	assert.Equal(t, map[string]string{
		"success/":           "",
		"rejected/not_found": "",
		"error/internal":     "",
		"error/canceled":     "",
	}, got)
}

// Технический сбой помечает span ошибкой, бизнес-отказ — нет; текст ошибки
// (может содержать вход) в span не попадает.
func TestInstrumentMarksOnlyTechnicalFailuresAsSpanErrors(t *testing.T) {
	p := install(t)
	handler := telemetry.Instrument(telemetry.NewUseCases(classify), telemetry.Query("get_order"),
		func(ctx context.Context, in string) (struct{}, error) {
			if in == "rejected" {
				return struct{}{}, errRejected
			}
			return struct{}{}, errors.New("password=secret")
		})

	_, _ = handler.Handle(context.Background(), "rejected")
	_, _ = handler.Handle(context.Background(), "broken")

	spans := p.spans.Ended()
	require.Len(t, spans, 2)
	for _, span := range spans {
		assert.Equal(t, "usecase get_order", span.Name())
		assert.Empty(t, span.Events())
		for _, kv := range span.Attributes() {
			assert.NotContains(t, kv.Value.String(), "secret")
		}
	}
	assert.Equal(t, codes.Unset, spans[0].Status().Code)
	assert.Equal(t, codes.Error, spans[1].Status().Code)
	assert.Equal(t, "internal", spans[1].Status().Description)
}
