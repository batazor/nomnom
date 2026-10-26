// Package vo holds the value objects of a Cell (ADR 41): its id and its
// Router — the Kubernetes Service of the Cosmo Router of the Cell that the
// public gateway routes Markets to.
package vo

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidValue marks a value that breaks the rules of its value object.
var ErrInvalidValue = errors.New("invalid value")

var (
	cellIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	// dnsLabelPattern is a DNS-1123 label: the names of a namespace and a Service.
	dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

// CellID identifies a Cell: slug ^[a-z][a-z0-9-]{1,31}$, immutable.
type CellID string

func NewCellID(raw string) (CellID, error) {
	if !cellIDPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: cell_id %q must match %s", ErrInvalidValue, raw, cellIDPattern)
	}
	return CellID(raw), nil
}

// Router is the Kubernetes Service of the Cosmo Router of a Cell: the entry of
// the Cell for clients.
type Router struct {
	namespace string
	name      string
	port      int32
}

func NewRouter(namespace, name string, port int32) (Router, error) {
	for field, value := range map[string]string{"router.namespace": namespace, "router.name": name} {
		if !dnsLabelPattern.MatchString(value) {
			return Router{}, fmt.Errorf("%w: %s %q must be a DNS-1123 label", ErrInvalidValue, field, value)
		}
	}
	if port < 1 || port > 65535 {
		return Router{}, fmt.Errorf("%w: router.port %d must be between 1 and 65535", ErrInvalidValue, port)
	}
	return Router{namespace: namespace, name: name, port: port}, nil
}

func (r Router) Namespace() string { return r.namespace }
func (r Router) Name() string      { return r.name }
func (r Router) Port() int32       { return r.port }

func (r Router) String() string { return fmt.Sprintf("%s/%s:%d", r.namespace, r.name, r.port) }
