package main

import (
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// internalError logs the cause and gives the client no internal details.
func internalError(operation string, err error) error {
	log.Printf("%s failed: %v", operation, err)
	return status.Error(codes.Unavailable, fmt.Sprintf("%s is temporarily unavailable", operation))
}
