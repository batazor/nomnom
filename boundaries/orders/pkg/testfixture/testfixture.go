// Package testfixture starts the infrastructure of integration tests once per
// test package, from TestMain:
//
//	var pg *testfixture.Postgres
//
//	func TestMain(m *testing.M) {
//		pg = testfixture.MustStartPostgres()
//		defer pg.Close()
//		m.Run()
//	}
//
// Tests of one package share the container and isolate their data by unique
// identifiers. Production code does not import this package.
package testfixture

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	// PostgresImage is POSTGRES_IMAGE of the Makefile.
	PostgresImage = "postgres:18.6-alpine3.24"
	// KafkaImage is the Apache Kafka version of the Strimzi broker of
	// ops/kind/manifests/messaging/kafka.yaml.
	KafkaImage = "apache/kafka:4.3.1"
	// SpiceDBImage is SPICEDB_IMAGE of the root Makefile.
	SpiceDBImage = "authzed/spicedb:v1.57.0"
	// SchemaRegistryImage is the Confluent Schema Registry of
	// ops/kind/manifests/messaging/schema-registry.yaml.
	SchemaRegistryImage = "docker.io/confluentinc/cp-schema-registry:8.4.0@sha256:12cd2770fa25df65243dabafd5eef5337ea1145a3cb85afc8a27f7151dfa2999"
	// TemporalImage is the Temporal CLI dev server of ops/kind/manifests/messaging/infrastructure.yaml.
	TemporalImage = "temporalio/temporal:1.9.1"
	// SpiceDBPresharedKey is the key of the SpiceDB container.
	SpiceDBPresharedKey = "devkey"
)

const startupTimeout = 2 * time.Minute

// Postgres is a PostgreSQL database for the tests of one package.
type Postgres struct {
	DSN       string
	container testcontainers.Container
}

// StartPostgres starts a PostgreSQL container. Migrations are the caller's:
// the fixture does not know the schema.
func StartPostgres(ctx context.Context) (*Postgres, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        PostgresImage,
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     "orders",
				"POSTGRES_PASSWORD": "orders",
				"POSTGRES_DB":       "orders",
			},
			// The entrypoint restarts the server after init: the second line is final.
			WaitingFor: wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithDeadline(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return nil, terminate(c, fmt.Errorf("start postgres: %w", err))
	}
	endpoint, err := loopbackEndpoint(ctx, c, "5432/tcp")
	if err != nil {
		return nil, terminate(c, fmt.Errorf("postgres endpoint: %w", err))
	}
	return &Postgres{DSN: "postgres://orders:orders@" + endpoint + "/orders?sslmode=disable", container: c}, nil
}

// MustStartPostgres is StartPostgres for TestMain: without the database no test
// of the package can run.
func MustStartPostgres() *Postgres {
	pg, err := StartPostgres(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return pg
}

// Close removes the container.
func (p *Postgres) Close() { closeContainer(p.container) }

// Kafka is a single-node KRaft broker for the tests of one package.
type Kafka struct {
	Brokers   []string
	container testcontainers.Container
}

// StartKafka runs the broker. The advertised listener must be the host-mapped
// address, so the host port is chosen before the container starts.
func StartKafka(ctx context.Context) (*Kafka, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(loopback, "0"))
	if err != nil {
		return nil, fmt.Errorf("choose kafka port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return nil, fmt.Errorf("choose kafka port: %w", err)
	}

	broker := fmt.Sprintf("127.0.0.1:%d", port)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        KafkaImage,
			ExposedPorts: []string{"9092/tcp"},
			Env: map[string]string{
				"KAFKA_NODE_ID":                                  "1",
				"KAFKA_PROCESS_ROLES":                            "broker,controller",
				"KAFKA_LISTENERS":                                "PLAINTEXT://:9092,CONTROLLER://:9093",
				"KAFKA_ADVERTISED_LISTENERS":                     "PLAINTEXT://" + broker,
				"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
				"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
				"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
				"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
				"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
				"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
				"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0",
			},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.PortBindings = network.PortMap{
					network.MustParsePort("9092/tcp"): {{HostIP: netip.MustParseAddr(loopback), HostPort: fmt.Sprint(port)}},
				}
			},
			WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return nil, terminate(c, fmt.Errorf("start kafka: %w", err))
	}
	return &Kafka{Brokers: []string{broker}, container: c}, nil
}

// MustStartKafka is StartKafka for TestMain.
func MustStartKafka() *Kafka {
	k, err := StartKafka(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return k
}

func (k *Kafka) Close() { closeContainer(k.container) }

// SchemaRegistry is a Confluent Schema Registry with a Kafka broker of its own
// for the tests of one package: the tests register the event files their
// records reference.
type SchemaRegistry struct {
	URL        string
	containers []testcontainers.Container
	network    *testcontainers.DockerNetwork
}

// StartSchemaRegistry runs Kafka and Schema Registry on a network of their own:
// the Registry stores schemas in the _schemas topic of the broker.
func StartSchemaRegistry(ctx context.Context) (*SchemaRegistry, error) {
	net, err := tcnetwork.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("schema registry network: %w", err)
	}
	r := &SchemaRegistry{network: net}
	broker, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          KafkaImage,
			Networks:       []string{net.Name},
			NetworkAliases: map[string][]string{net.Name: {"kafka"}},
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
			WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	if broker != nil {
		r.containers = append(r.containers, broker)
	}
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("start schema registry kafka: %w", err)
	}
	registry, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        SchemaRegistryImage,
			Networks:     []string{net.Name},
			ExposedPorts: []string{"8081/tcp"},
			Env: map[string]string{
				"SCHEMA_REGISTRY_HOST_NAME":                    "schema-registry",
				"SCHEMA_REGISTRY_LISTENERS":                    "http://0.0.0.0:8081",
				"SCHEMA_REGISTRY_KAFKASTORE_BOOTSTRAP_SERVERS": "PLAINTEXT://kafka:9092",
			},
			WaitingFor: wait.ForHTTP("/subjects").WithPort("8081/tcp").WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	if registry != nil {
		r.containers = append(r.containers, registry)
	}
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("start schema registry: %w", err)
	}
	endpoint, err := loopbackEndpoint(ctx, registry, "8081/tcp")
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("schema registry endpoint: %w", err)
	}
	r.URL = "http://" + endpoint
	return r, nil
}

// MustStartSchemaRegistry is StartSchemaRegistry for TestMain.
func MustStartSchemaRegistry() *SchemaRegistry {
	r, err := StartSchemaRegistry(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return r
}

// RegisterEvents registers the .proto source of the event file of the
// repository as schema-registration does (FR-712, FR-717): imported files of
// the modules first under their path, then the file under the full name of
// each top-level message. It returns the schema id of the file.
func (r *SchemaRegistry) RegisterEvents(ctx context.Context, file protoreflect.FileDescriptor) (uint32, error) {
	s, err := r.schemaOf(ctx, file)
	if err != nil {
		return 0, err
	}
	var id uint32
	messages := file.Messages()
	for i := range messages.Len() {
		registered, err := r.post(ctx, "/subjects/"+string(messages.Get(i).FullName())+"/versions", s)
		if err != nil {
			return 0, err
		}
		id = registered.ID
	}
	return id, nil
}

type registrySchema struct {
	SchemaType string              `json:"schemaType"`
	Schema     string              `json:"schema"`
	References []registryReference `json:"references,omitempty"`
}

type registryReference struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	Version int    `json:"version"`
}

func (r *SchemaRegistry) schemaOf(ctx context.Context, file protoreflect.FileDescriptor) (registrySchema, error) {
	// nomnom/<context>/v1/x.proto lives in the buf module of the context.
	_, self, _, _ := runtime.Caller(0)
	parts := strings.SplitN(file.Path(), "/", 3)
	if len(parts) != 3 || parts[0] != "nomnom" {
		return registrySchema{}, fmt.Errorf("%s is not a file of a context module", file.Path())
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(self), "..", "..", "..", parts[1], "infrastructure", "grpc", file.Path()))
	if err != nil {
		return registrySchema{}, fmt.Errorf("read %s: %w", file.Path(), err)
	}
	s := registrySchema{SchemaType: "PROTOBUF", Schema: string(source)}
	imports := file.Imports()
	for i := range imports.Len() {
		imported := imports.Get(i).FileDescriptor
		if strings.HasPrefix(imported.Path(), "google/protobuf/") {
			continue
		}
		dependency, err := r.schemaOf(ctx, imported)
		if err != nil {
			return registrySchema{}, err
		}
		subject := url.PathEscape(imported.Path())
		if _, err := r.post(ctx, "/subjects/"+subject+"/versions", dependency); err != nil {
			return registrySchema{}, err
		}
		found, err := r.post(ctx, "/subjects/"+subject, dependency)
		if err != nil {
			return registrySchema{}, err
		}
		s.References = append(s.References, registryReference{Name: imported.Path(), Subject: imported.Path(), Version: found.Version})
	}
	return s, nil
}

type registered struct {
	ID      uint32 `json:"id"`
	Version int    `json:"version"`
}

func (r *SchemaRegistry) post(ctx context.Context, path string, s registrySchema) (registered, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return registered{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+path, bytes.NewReader(body))
	if err != nil {
		return registered{}, err
	}
	req.Header.Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return registered{}, fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return registered{}, fmt.Errorf("POST %s: status %d", path, resp.StatusCode)
	}
	var out registered
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return registered{}, fmt.Errorf("POST %s: %w", path, err)
	}
	return out, nil
}

// Close removes the containers and their network.
func (r *SchemaRegistry) Close() {
	for _, c := range slices.Backward(r.containers) {
		closeContainer(c)
	}
	if r.network != nil {
		_ = r.network.Remove(context.Background())
	}
}

// WireFormat frames a Protobuf payload as the SchemaFraming SMT of the
// connectors does (FR-714): magic byte, schema id, the indexes of the
// top-level message and the payload.
func WireFormat(schemaID uint32, messageIndex int, payload []byte) []byte {
	framed := []byte{0}
	framed = binary.BigEndian.AppendUint32(framed, schemaID)
	if messageIndex == 0 {
		framed = append(framed, 0)
	} else {
		framed = binary.AppendVarint(framed, 1)
		framed = binary.AppendVarint(framed, int64(messageIndex))
	}
	return append(framed, payload...)
}

// SpiceDB is an in-memory SpiceDB with authorization/bootstrap.yaml of the
// repository: the schema plus the grants deployment owns, as in kind.
type SpiceDB struct {
	Endpoint  string
	container testcontainers.Container
}

// StartSpiceDB runs SpiceDB with the bootstrap of `make spicedb-dev`.
func StartSpiceDB(ctx context.Context) (*SpiceDB, error) {
	_, file, _, _ := runtime.Caller(0)
	authz := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "authorization")
	files := make([]testcontainers.ContainerFile, 0, 2)
	for _, name := range []string{"bootstrap.yaml", "schema.zed"} {
		files = append(files, testcontainers.ContainerFile{HostFilePath: filepath.Join(authz, name), ContainerFilePath: "/authz/" + name, FileMode: 0o644})
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        SpiceDBImage,
			ExposedPorts: []string{"50051/tcp"},
			Files:        files,
			WorkingDir:   "/authz",
			Cmd: []string{"serve", "--grpc-preshared-key", SpiceDBPresharedKey,
				"--datastore-engine", "memory", "--datastore-bootstrap-files", "bootstrap.yaml"},
			WaitingFor: wait.ForAll(
				wait.ForLog("grpc server started serving"),
				wait.ForListeningPort("50051/tcp"),
			).WithDeadline(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return nil, terminate(c, fmt.Errorf("start spicedb: %w", err))
	}
	endpoint, err := loopbackEndpoint(ctx, c, "50051/tcp")
	if err != nil {
		return nil, terminate(c, fmt.Errorf("spicedb endpoint: %w", err))
	}
	return &SpiceDB{Endpoint: endpoint, container: c}, nil
}

// MustStartSpiceDB is StartSpiceDB for TestMain.
func MustStartSpiceDB() *SpiceDB {
	s, err := StartSpiceDB(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return s
}

func (s *SpiceDB) Close() { closeContainer(s.container) }

// Temporal is a Temporal dev server (`temporal server start-dev`) with the
// default namespace and Task Queue Fairness enabled, as in kind.
type Temporal struct {
	Address   string
	container testcontainers.Container
}

// StartTemporal runs the Temporal dev server of TemporalImage.
func StartTemporal(ctx context.Context) (*Temporal, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        TemporalImage,
			ExposedPorts: []string{"7233/tcp"},
			Cmd:          []string{"server", "start-dev", "--ip", "0.0.0.0", "--headless", "--dynamic-config-value", "matching.enableFairness=true"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("7233/tcp"),
				wait.ForExec([]string{"temporal", "operator", "namespace", "describe", "--namespace", "default"}),
			).WithDeadline(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return nil, terminate(c, fmt.Errorf("start temporal: %w", err))
	}
	endpoint, err := loopbackEndpoint(ctx, c, "7233/tcp")
	if err != nil {
		return nil, terminate(c, fmt.Errorf("temporal endpoint: %w", err))
	}
	return &Temporal{Address: endpoint, container: c}, nil
}

// MustStartTemporal is StartTemporal for TestMain.
func MustStartTemporal() *Temporal {
	tc, err := StartTemporal(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return tc
}

func (t *Temporal) Close() { closeContainer(t.container) }

// loopback is the address of the ports Docker publishes for the tests. An IP
// literal keeps clients off the DNS of the host: the gRPC resolver looks up
// TXT _grpc_config.<host> for a name, which a slow local resolver answers in
// seconds.
const loopback = "127.0.0.1"

// loopbackEndpoint is "127.0.0.1:<published port>" of a container port.
func loopbackEndpoint(ctx context.Context, c testcontainers.Container, port string) (string, error) {
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(loopback, mapped.Port()), nil
}

// terminate removes a container that failed to start, keeping the start error.
func terminate(c testcontainers.Container, err error) error {
	closeContainer(c)
	return err
}

func closeContainer(c testcontainers.Container) {
	if c == nil {
		return
	}
	if err := testcontainers.TerminateContainer(c); err != nil {
		log.Printf("terminate test container: %v", err)
	}
}
