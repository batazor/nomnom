package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// errIncompatible is the 409 of the Schema Registry: the new version breaks
// the compatibility level of the subject.
var errIncompatible = errors.New("schema is incompatible with registered versions")

// reference links a schema to a registered schema it imports.
type reference struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Version int    `json:"version"`
}

type schema struct {
	SchemaType string      `json:"schemaType"`
	Schema     string      `json:"schema"`
	References []reference `json:"references,omitempty"`
}

// registry is a client of the Confluent-compatible Schema Registry API.
type registry struct {
	base   *url.URL
	client *http.Client
}

func newRegistry(rawURL string, client *http.Client) (registry, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return registry{}, fmt.Errorf("parse schema registry url: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" || base.Host == "" {
		return registry{}, fmt.Errorf("schema registry url %q must be an absolute http(s) url", rawURL)
	}
	return registry{base: base, client: client}, nil
}

// setCompatibility sets the global compatibility level.
func (r registry) setCompatibility(ctx context.Context, level string) error {
	return r.do(ctx, http.MethodPut, "config", map[string]string{"compatibility": level}, nil)
}

// register adds the schema to the subject and returns its version; a schema
// already registered under the subject returns the existing version.
func (r registry) register(ctx context.Context, subject string, s schema) (int, error) {
	if err := r.do(ctx, http.MethodPost, "subjects/"+url.PathEscape(subject)+"/versions", s, nil); err != nil {
		return 0, fmt.Errorf("register %s: %w", subject, err)
	}
	// The register response has only the id in the Confluent API; the version
	// for references comes from the lookup of the same schema.
	var found struct {
		Version int `json:"version"`
	}
	if err := r.do(ctx, http.MethodPost, "subjects/"+url.PathEscape(subject), s, &found); err != nil {
		return 0, fmt.Errorf("look up %s: %w", subject, err)
	}
	return found.Version, nil
}

func (r registry) do(ctx context.Context, method, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	// JoinPath takes escaped elements: %2F of file subjects stays in RawPath.
	endpoint := r.base.JoinPath(path)
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusConflict:
		return fmt.Errorf("%w: %s", errIncompatible, raw)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	case out == nil:
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
