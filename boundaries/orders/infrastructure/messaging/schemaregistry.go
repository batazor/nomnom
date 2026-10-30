package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

// ErrUnknownSchema is a schema id the Registry does not know: the record
// cannot be read by any redelivery.
var ErrUnknownSchema = errors.New("schema id is unknown to the schema registry")

// SchemaSubjects resolves the subjects of a schema id. The subject of an
// Integration Event is its event_type (FR-717).
type SchemaSubjects interface {
	Subjects(ctx context.Context, schemaID uint32) ([]string, error)
}

// SchemaRegistry reads the subjects of schema ids from the Confluent-compatible
// Schema Registry. Schema ids are immutable, so a resolved id is kept for the
// life of the process.
type SchemaRegistry struct {
	base   *url.URL
	client *http.Client
	known  sync.Map // uint32 → []string
}

var _ SchemaSubjects = (*SchemaRegistry)(nil)

func NewSchemaRegistry(rawURL string, client *http.Client) (*SchemaRegistry, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse schema registry url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" || base.Host == "" {
		return nil, fmt.Errorf("schema registry url %q must be an absolute http(s) url", rawURL)
	}
	return &SchemaRegistry{base: base, client: client}, nil
}

// Subjects returns ErrUnknownSchema for an id the Registry does not have; any
// other error is transient: the Registry is unavailable.
func (r *SchemaRegistry) Subjects(ctx context.Context, schemaID uint32) ([]string, error) {
	if subjects, ok := r.known.Load(schemaID); ok {
		return subjects.([]string), nil
	}
	endpoint := r.base.JoinPath("schemas", "ids", strconv.FormatUint(uint64(schemaID), 10), "versions")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build schema registry request: %w", err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("schema registry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read schema registry response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %d", ErrUnknownSchema, schemaID)
	default:
		return nil, fmt.Errorf("schema registry: subjects of schema %d: status %d: %s", schemaID, resp.StatusCode, body)
	}
	var versions []struct {
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(body, &versions); err != nil {
		return nil, fmt.Errorf("decode subjects of schema %d: %w", schemaID, err)
	}
	subjects := make([]string, len(versions))
	for i, version := range versions {
		subjects[i] = version.Subject
	}
	r.known.Store(schemaID, subjects)
	return subjects, nil
}
