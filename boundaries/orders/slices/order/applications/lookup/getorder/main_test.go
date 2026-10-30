package getorder_test

import (
	"testing"

	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// TestMain shares one PostgreSQL server between the tests of the package.
func TestMain(m *testing.M) { repositorytest.Main(m) }
