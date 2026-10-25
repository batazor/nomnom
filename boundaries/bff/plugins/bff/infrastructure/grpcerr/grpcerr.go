// Package grpcerr reads the google.rpc.ErrorInfo detail that bounded contexts
// attach to their gRPC errors. Adapters decide per operation what a reason
// means; this package only extracts it.
package grpcerr

import (
	"strings"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// Info returns the reason and metadata of the first ErrorInfo detail of err;
// ok is false when err is not a gRPC status or carries no ErrorInfo.
func Info(err error) (reason string, metadata map[string]string, ok bool) {
	st, isStatus := status.FromError(err)
	if !isStatus {
		return "", nil, false
	}
	for _, detail := range st.Details() {
		if info, isInfo := detail.(*errdetails.ErrorInfo); isInfo {
			return info.GetReason(), info.GetMetadata(), true
		}
	}
	return "", nil, false
}

// List reads a comma-separated metadata value; empty means none.
func List(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
