package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config"
	"nomnom/orders/slices/order/infrastructure/repository"
)

func TestOpenRequiresDSN(t *testing.T) {
	_, err := repository.Open(context.Background(), config.PostgresConfig{})
	require.ErrorContains(t, err, config.EnvPostgresDSN)
}
