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
	"context"
	"fmt"
	"log"
	"net"
	"net/netip"
	"path/filepath"
	"runtime"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// PostgresImage is POSTGRES_IMAGE of the Makefile.
	PostgresImage = "postgres:18.6-alpine3.24"
	// KafkaImage is the Kafka of ops/kind/manifests/messaging/infrastructure.yaml.
	KafkaImage = "apache/kafka:4.4.0"
	// SpiceDBImage is SPICEDB_IMAGE of the root Makefile.
	SpiceDBImage = "authzed/spicedb:v1.57.0"
	// TemporalImage is TEMPORAL_IMAGE of the Makefile (Temporal CLI dev server).
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
	endpoint, err := c.PortEndpoint(ctx, "5432/tcp", "")
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
					network.MustParsePort("9092/tcp"): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: fmt.Sprint(port)}},
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
	endpoint, err := c.PortEndpoint(ctx, "50051/tcp", "")
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
// default namespace.
type Temporal struct {
	Address   string
	container testcontainers.Container
}

// StartTemporal runs the dev server of `make temporal-dev`.
func StartTemporal(ctx context.Context) (*Temporal, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        TemporalImage,
			ExposedPorts: []string{"7233/tcp"},
			Cmd:          []string{"server", "start-dev", "--ip", "0.0.0.0", "--headless"},
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
	endpoint, err := c.PortEndpoint(ctx, "7233/tcp", "")
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
