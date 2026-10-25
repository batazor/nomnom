//go:build integration

package messaging_test

import (
	"testing"

	"nomnom/orders/pkg/testfixture"
)

// kafka is the broker of the package, started once for all integration tests.
var kafka *testfixture.Kafka

func TestMain(m *testing.M) {
	kafka = testfixture.MustStartKafka()
	defer kafka.Close()
	m.Run()
}
