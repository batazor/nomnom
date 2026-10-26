// Package vo holds the value objects of a Cell (ADR 41): its id and the address
// of its internal gateway.
package vo

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// ErrInvalidValue marks a value that breaks the rules of its value object.
var ErrInvalidValue = errors.New("invalid value")

var cellIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// CellID identifies a Cell: slug ^[a-z][a-z0-9-]{1,31}$, immutable.
type CellID string

func NewCellID(raw string) (CellID, error) {
	if !cellIDPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: cell_id %q must match %s", ErrInvalidValue, raw, cellIDPattern)
	}
	return CellID(raw), nil
}

// GatewayAddress is host:port of the internal gateway of a Cell (ADR 15).
type GatewayAddress string

func NewGatewayAddress(raw string) (GatewayAddress, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("%w: gateway_address %q must be host:port: %w", ErrInvalidValue, raw, err)
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n/") {
		return "", fmt.Errorf("%w: gateway_address %q needs a host", ErrInvalidValue, raw)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", fmt.Errorf("%w: gateway_address %q needs a port between 1 and 65535", ErrInvalidValue, raw)
	}
	return GatewayAddress(raw), nil
}
