package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
)

// A section loader reads only its section: orders-admin needs no configuration
// of the API to run.
func TestSectionLoadersReadOnlyTheirSection(t *testing.T) {
	t.Setenv("POSTGRES__DSN", "postgres://orders@localhost/orders")
	t.Setenv("POSTGRES__MAX_CONNS", "2")
	t.Setenv("POSTGRES__CONNECT_TIMEOUT", "3s")
	t.Setenv("KAFKA__BROKERS", "kafka:9092")
	t.Setenv("KAFKA__CLIENT_ID", "orders-admin")
	t.Setenv("KAFKA__CONSUMER_GROUP", "orders")

	db, err := config.LoadDatabase()
	require.NoError(t, err)
	assert.Equal(t, "postgres://orders@localhost/orders", db.DSN)
	assert.Equal(t, int32(2), db.MaxConns)
	assert.Equal(t, 3*time.Second, db.ConnectTimeout)

	kafka, err := config.LoadKafka()
	require.NoError(t, err)
	assert.Equal(t, []string{"kafka:9092"}, kafka.Brokers)
	assert.Equal(t, "orders", kafka.ConsumerGroup)

	t.Setenv("POSTGRES__MAX_CONNS", "0")
	_, err = config.LoadDatabase()
	require.Error(t, err, "an invalid section fails the command")
}
