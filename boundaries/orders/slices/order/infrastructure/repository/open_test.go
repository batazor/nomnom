package repository_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config/database"
	"nomnom/orders/slices/order/infrastructure/repository"
)

func TestOpenRequiresDSN(t *testing.T) {
	_, err := repository.Open(context.Background(), database.Config{})
	require.ErrorContains(t, err, database.EnvPostgresDSN)
}
