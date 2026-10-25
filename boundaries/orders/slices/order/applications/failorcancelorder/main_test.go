package failorcancelorder_test

import (
	"testing"

	"nomnom/orders/slices/order/infrastructure/postgres/postgrestest"
)

// TestMain shares one PostgreSQL server between the tests of the package.
func TestMain(m *testing.M) { postgrestest.Main(m) }
