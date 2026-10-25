package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/postgres"
)

func TestOpenRequiresDSN(t *testing.T) {
	_, err := postgres.Open(context.Background(), config.PostgresConfig{})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}
