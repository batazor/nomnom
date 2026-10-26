package di

import (
	"testing"

	"github.com/stretchr/testify/require"

	"nomnom/orders/pkg/config/database"
)

func TestNewStoreRequiresDSN(t *testing.T) {
	_, _, err := NewStore(database.Config{MaxConns: 1})
	require.ErrorContains(t, err, database.EnvPostgresDSN)
}
