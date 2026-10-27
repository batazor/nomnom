package spicedb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/infrastructure/spicedb"
)

// installMeter sets a test MeterProvider before NewAuthorizer builds its
// instruments: global instruments bind to the first provider they see.
func installMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

type checkPoint struct {
	permission, resourceType, result string
}

func point(t *testing.T, set attribute.Set) checkPoint {
	t.Helper()
	value := func(key attribute.Key) string {
		v, ok := set.Value(key)
		require.True(t, ok, string(key))
		return v.AsString()
	}
	assert.Equal(t, 3, set.Len(), "only permission, resource_type and result: %v", set)
	return checkPoint{value("nomnom.authz.permission"), value("nomnom.authz.resource_type"), value("nomnom.authz.result")}
}

// Имена и attributes — контракт ops/grafana/inventory (раздел authz): denied —
// ответ SpiceDB, error и timeout — сбой проверки; ID subject и resource не пишутся.
func TestCheckRecordsResult(t *testing.T) {
	for _, tc := range []struct {
		result         string
		permissionship v1.CheckPermissionResponse_Permissionship
		err            error
	}{
		{result: "allowed", permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION},
		{result: "denied", permissionship: v1.CheckPermissionResponse_PERMISSIONSHIP_NO_PERMISSION},
		{result: "error", err: errors.New("unavailable")},
		{result: "timeout", err: status.Error(codes.DeadlineExceeded, "context deadline exceeded")},
	} {
		t.Run(tc.result, func(t *testing.T) {
			reader := installMeter(t)
			permissions := NewMockPermissions(t)
			permissions.EXPECT().CheckPermission(mock.Anything, mock.MatchedBy(func(r *v1.CheckPermissionRequest) bool {
				return proto.Equal(r, customerCheckRequest)
			})).
				Return(&v1.CheckPermissionResponse{Permissionship: tc.permissionship}, tc.err).Once()
			a := spicedb.NewAuthorizer(permissions, nil, time.Second)

			_ = a.Check(context.Background(), customerCheck)

			var rm metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(context.Background(), &rm))
			want := checkPoint{"view", "order", tc.result}
			var counted, timed bool
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					switch m.Name {
					case "nomnom.authz.checks":
						assert.Equal(t, "{check}", m.Unit)
						points := m.Data.(metricdata.Sum[int64]).DataPoints
						require.Len(t, points, 1)
						assert.Equal(t, want, point(t, points[0].Attributes))
						assert.EqualValues(t, 1, points[0].Value)
						counted = true
					case "nomnom.authz.check.duration":
						assert.Equal(t, "s", m.Unit)
						points := m.Data.(metricdata.Histogram[float64]).DataPoints
						require.Len(t, points, 1)
						assert.Equal(t, want, point(t, points[0].Attributes))
						assert.EqualValues(t, 1, points[0].Count)
						assert.Equal(t, telemetry.SecondsBuckets, points[0].Bounds)
						timed = true
					}
				}
			}
			assert.True(t, counted, "nomnom.authz.checks")
			assert.True(t, timed, "nomnom.authz.check.duration")
		})
	}
}
