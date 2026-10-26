package main

import (
	"errors"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/cellrouter"
)

// internalError logs the cause and gives the client no internal details. A
// request the cell router refused (ADR 41) is the client's to fix or retry:
// its status — INVALID_ARGUMENT, NOT_FOUND or UNAVAILABLE — reaches the
// client as is.
func internalError(operation string, err error) error {
	if refusal, ok := errors.AsType[*cellrouter.Refusal](err); ok {
		return refusal.Err()
	}
	log.Printf("%s failed: %v", operation, err)
	return status.Error(codes.Unavailable, fmt.Sprintf("%s is temporarily unavailable", operation))
}
