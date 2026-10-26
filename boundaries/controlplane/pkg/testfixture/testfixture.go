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
// Production code does not import this package.
package testfixture

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// PostgresImage is POSTGRES_IMAGE of the Makefile.
	PostgresImage = "postgres:18.6-alpine3.24"
	// SpiceDBImage is SPICEDB_IMAGE of the root Makefile.
	SpiceDBImage = "authzed/spicedb:v1.57.0"
	// SpiceDBPresharedKey is the key of the SpiceDB container.
	SpiceDBPresharedKey = "devkey"
)

const startupTimeout = 2 * time.Minute

// Postgres is a PostgreSQL server for the tests of one package.
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
				"POSTGRES_USER":     "controlplane",
				"POSTGRES_PASSWORD": "controlplane",
				"POSTGRES_DB":       "controlplane",
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
	return &Postgres{DSN: "postgres://controlplane:controlplane@" + endpoint + "/controlplane?sslmode=disable", container: c}, nil
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

func (p *Postgres) Close() { closeContainer(p.container) }

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
