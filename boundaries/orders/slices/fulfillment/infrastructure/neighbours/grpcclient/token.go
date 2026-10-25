package grpcclient

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// audienceByPackage maps the proto package of a method to the audience of the
// target context.
var audienceByPackage = map[string]string{
	"nomnom.payments.v1":    "nomnom.payments",
	"nomnom.restaurants.v1": "nomnom.restaurants",
	"nomnom.delivery.v1":    "nomnom.delivery",
}

// ServiceToken attaches `authorization: Bearer` with the projected token of
// the target audience. The file is read on every call because kubelet rotates it;
// a missing token fails the call, which the Activity retries.
func ServiceToken(dir string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		audience, err := audienceOf(method)
		if err != nil {
			return err
		}
		token, err := os.ReadFile(filepath.Join(dir, audience))
		if err != nil {
			return fmt.Errorf("read service token for %s: %w", audience, err)
		}
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+strings.TrimSpace(string(token)))
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func audienceOf(method string) (string, error) {
	service, _, _ := strings.Cut(strings.TrimPrefix(method, "/"), "/")
	pkg := service[:max(strings.LastIndex(service, "."), 0)]
	audience, ok := audienceByPackage[pkg]
	if !ok {
		return "", fmt.Errorf("no service token audience for method %s", method)
	}
	return audience, nil
}
