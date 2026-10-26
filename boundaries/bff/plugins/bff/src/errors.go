package main

import (
	"errors"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/market"
)

// internalError logs the cause and gives the client no internal details. A
// request refused for its Market (ADR 41) is the client's to fix: its status,
// INVALID_ARGUMENT, reaches the client as is.
func internalError(operation string, err error) error {
	if refusal, ok := errors.AsType[*market.Refusal](err); ok {
		return refusal.Err()
	}
	log.Printf("%s failed: %v", operation, err)
	return status.Error(codes.Unavailable, fmt.Sprintf("%s is temporarily unavailable", operation))
}
