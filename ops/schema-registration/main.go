// Command schema-registration registers the Protobuf schemas of Integration
// Events from the buf modules of the contexts in the Schema Registry (FR-712,
// FR-713, FR-717). It is the only writer of the Registry: producers, the
// SchemaFraming SMT and consumers only read it.
//
//	SCHEMA_REGISTRY__URL=http://kafka:8081 schema-registration --root . --config ops/schema-registration/event-files.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

// EnvSchemaRegistryURL is the required address of the Registry.
const EnvSchemaRegistryURL = "SCHEMA_REGISTRY__URL"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "schema-registration:", err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", "", "directory the modules of the config are relative to")
	configPath := flag.String("config", "", "event files config (event-files.yaml)")
	flag.Parse()
	if *root == "" || *configPath == "" {
		return fmt.Errorf("--root and --config are required")
	}
	rawURL := os.Getenv(EnvSchemaRegistryURL)
	if rawURL == "" {
		return fmt.Errorf("%s is required", EnvSchemaRegistryURL)
	}
	reg, err := newRegistry(rawURL, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		return err
	}
	cfg, err := loadEventFiles(*configPath)
	if err != nil {
		return err
	}
	r := &registrar{root: *root, modules: cfg.Modules, registry: reg, versions: map[string]int{}}
	return r.run(context.Background(), cfg.Files)
}
