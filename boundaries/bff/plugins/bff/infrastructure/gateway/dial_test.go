package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// ADR 15: a call without a deadline of the caller gets the call timeout.
func TestDeadlineBoundsACallWithoutDeadline(t *testing.T) {
	var deadline time.Time
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		var ok bool
		deadline, ok = ctx.Deadline()
		require.True(t, ok)
		return nil
	}

	start := time.Now()
	require.NoError(t, Deadline(5*time.Second)(context.Background(), "/nomnom.orders.v1.OrderService/GetOrder", nil, nil, nil, invoker))
	assert.WithinRange(t, deadline, start.Add(5*time.Second), time.Now().Add(5*time.Second))
}

// An earlier deadline of the caller stays.
func TestDeadlineKeepsAnEarlierDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		deadline, _ := ctx.Deadline()
		assert.Equal(t, want, deadline)
		return nil
	}

	require.NoError(t, Deadline(5*time.Second)(ctx, "/nomnom.orders.v1.OrderService/GetOrder", nil, nil, nil, invoker))
}
