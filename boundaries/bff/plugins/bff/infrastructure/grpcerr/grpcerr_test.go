package grpcerr

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInfoReadsReasonAndMetadata(t *testing.T) {
	st, err := status.New(codes.FailedPrecondition, "refused").WithDetails(&errdetails.ErrorInfo{
		Reason: "ORDER_NOT_ORDERABLE", Domain: "nomnom.orders.v1", Metadata: map[string]string{"codes": "A,B"},
	})
	require.NoError(t, err)
	reason, metadata, ok := Info(st.Err())
	assert.True(t, ok)
	assert.Equal(t, "ORDER_NOT_ORDERABLE", reason)
	assert.Equal(t, []string{"A", "B"}, List(metadata["codes"]))
}

func TestInfoWithoutErrorInfo(t *testing.T) {
	_, _, ok := Info(status.Error(codes.FailedPrecondition, "no details"))
	assert.False(t, ok)
	_, _, ok = Info(errors.New("not a status"))
	assert.False(t, ok)
	assert.Nil(t, List(""))
}
