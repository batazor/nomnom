//go:build !integration

package messaging_test

import (
	"testing"

	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

// TestMain shares one PostgreSQL server between the tests of the package; the
// integration build starts Kafka as well (main_test.go).
func TestMain(m *testing.M) { postgrestest.Main(m) }
