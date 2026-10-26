package messaging_test

import (
	"testing"

	"nomnom/orders/pkg/testfixture"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"
)

// kafka is the broker of the package, started once; PostgreSQL is shared through repositorytest.
var kafka *testfixture.Kafka

func TestMain(m *testing.M) {
	kafka = testfixture.MustStartKafka()
	defer kafka.Close()
	repositorytest.Main(m)
}
