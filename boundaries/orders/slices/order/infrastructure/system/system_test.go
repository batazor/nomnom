package system

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// AC-515: Order ids are UUIDv7.
func TestOrderIDsAreUUIDv7(t *testing.T) {
	first, err := OrderIDs{}.NewOrderID(context.Background())
	require.NoError(t, err)
	second, err := OrderIDs{}.NewOrderID(context.Background())
	require.NoError(t, err)

	parsed, err := uuid.Parse(string(first))
	require.NoError(t, err)
	require.Equal(t, uuid.Version(7), parsed.Version())
	require.NotEqual(t, first, second)
}
