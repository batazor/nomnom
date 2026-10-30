package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Kafka and Confluent Schema Registry of the kind environment
// (ops/kind/manifests/messaging/kafka.yaml, schema-registry.yaml).
const (
	kafkaImage          = "apache/kafka:4.3.1"
	schemaRegistryImage = "docker.io/confluentinc/cp-schema-registry:8.4.0@sha256:12cd2770fa25df65243dabafd5eef5337ea1145a3cb85afc8a27f7151dfa2999"
)

var registryURL string

// TestMain runs Kafka and Schema Registry on a network of their own: the
// Registry stores schemas in the _schemas topic of the broker.
func TestMain(m *testing.M) {
	ctx := context.Background()
	dockerNet, err := network.New(ctx)
	if err != nil {
		panic(err)
	}
	kafka, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          kafkaImage,
			Networks:       []string{dockerNet.Name},
			NetworkAliases: map[string][]string{dockerNet.Name: {"kafka"}},
			Env: map[string]string{
				"KAFKA_NODE_ID":                                  "1",
				"KAFKA_PROCESS_ROLES":                            "broker,controller",
				"KAFKA_LISTENERS":                                "PLAINTEXT://:9092,CONTROLLER://:9093",
				"KAFKA_ADVERTISED_LISTENERS":                     "PLAINTEXT://kafka:9092",
				"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
				"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
				"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
				"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
				"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
				"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
			},
			WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		panic(err)
	}
	registry, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        schemaRegistryImage,
			Networks:     []string{dockerNet.Name},
			ExposedPorts: []string{"8081/tcp"},
			Env: map[string]string{
				"SCHEMA_REGISTRY_HOST_NAME":                    "schema-registry",
				"SCHEMA_REGISTRY_LISTENERS":                    "http://0.0.0.0:8081",
				"SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS": "PLAINTEXT://kafka:9092",
			},
			WaitingFor: wait.ForHTTP("/subjects").WithPort("8081/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		panic(err)
	}
	port, err := registry.MappedPort(ctx, "8081/tcp")
	if err != nil {
		panic(err)
	}
	registryURL = "http://" + net.JoinHostPort("127.0.0.1", port.Port())
	code := m.Run()
	_ = registry.Terminate(ctx)
	_ = kafka.Terminate(ctx)
	_ = dockerNet.Remove(ctx)
	os.Exit(code)
}

func newTestRegistrar(t *testing.T, root string, modules []string) *registrar {
	t.Helper()
	reg, err := newRegistry(registryURL, &http.Client{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return &registrar{root: root, modules: modules, registry: reg, versions: map[string]int{}}
}

func getJSON(t *testing.T, path string, out any) {
	t.Helper()
	resp, err := http.Get(registryURL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

// AC-971, AC-972: the event files of the repository register under the full
// names of their messages; a second run adds no versions.
func TestRegistersEventFilesOfRepository(t *testing.T) {
	cfg, err := loadEventFiles("event-files.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := newTestRegistrar(t, "../..", cfg.Modules).run(context.Background(), cfg.Files); err != nil {
			t.Fatal(err)
		}
	}

	var compat struct {
		CompatibilityLevel string `json:"compatibilityLevel"`
	}
	getJSON(t, "/config", &compat)
	if compat.CompatibilityLevel != compatibility {
		t.Fatalf("compatibility = %s, want %s", compat.CompatibilityLevel, compatibility)
	}

	var ids []int
	for _, subject := range []string{
		"nomnom.delivery.v1.CourierAssignedToDelivery",
		"nomnom.delivery.v1.DeliveryPickedUp",
		"nomnom.delivery.v1.DeliveryCancelled",
	} {
		var versions []int
		getJSON(t, "/subjects/"+subject+"/versions", &versions)
		if len(versions) != 1 {
			t.Fatalf("%s versions = %v, want one after two runs", subject, versions)
		}
		var latest struct {
			ID int `json:"id"`
		}
		getJSON(t, "/subjects/"+subject+"/versions/latest", &latest)
		ids = append(ids, latest.ID)
	}
	if ids[0] != ids[1] || ids[1] != ids[2] {
		t.Fatalf("schema ids of delivery events = %v, want one id of events.proto", ids)
	}

	var reference struct {
		References []reference `json:"references"`
	}
	getJSON(t, "/subjects/nomnom.payments.v1.PaymentAuthorized/versions/latest", &reference)
	if len(reference.References) != 1 || reference.References[0].Subject != "nomnom/payments/v1/payments.proto" {
		t.Fatalf("references of PaymentAuthorized = %+v, want nomnom/payments/v1/payments.proto", reference.References)
	}
}

// AC-973: a field type change is refused with 409 and stops the run.
func TestRefusesIncompatibleVersion(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "module", "nomnom", "probe", "v1", "events.proto")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(fieldType string) {
		source := "syntax = \"proto3\";\npackage nomnom.probe.v1;\nmessage ProbeHappened {\n  " + fieldType + " probe_id = 1;\n}\n"
		if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{"nomnom/probe/v1/events.proto"}

	write("string")
	if err := newTestRegistrar(t, root, []string{"module"}).run(context.Background(), files); err != nil {
		t.Fatal(err)
	}
	write("int64")
	err := newTestRegistrar(t, root, []string{"module"}).run(context.Background(), files)
	if !errors.Is(err, errIncompatible) {
		t.Fatalf("err = %v, want errIncompatible", err)
	}
	var versions []int
	getJSON(t, "/subjects/nomnom.probe.v1.ProbeHappened/versions", &versions)
	if len(versions) != 1 {
		t.Fatalf("versions = %v, want the compatible one only", versions)
	}
}

func TestRequiresModulesAndFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "event-files.yaml")
	if err := os.WriteFile(path, []byte("modules: [a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEventFiles(path); err == nil || !strings.Contains(err.Error(), "must list modules and files") {
		t.Fatalf("err = %v, want missing files", err)
	}
}
