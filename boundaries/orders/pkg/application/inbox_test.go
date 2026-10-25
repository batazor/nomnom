package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInbox is a transactional Inbox: Do keeps its records only on success.
type fakeInbox struct {
	processed map[string]bool
	acceptErr error
}

func (f *fakeInbox) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	backup := make(map[string]bool, len(f.processed))
	for key, value := range f.processed {
		backup[key] = value
	}
	if err := fn(ctx); err != nil {
		f.processed = backup
		return err
	}
	return nil
}

func (f *fakeInbox) Accept(_ context.Context, consumer, eventID string) (bool, error) {
	if f.acceptErr != nil {
		return false, f.acceptErr
	}
	key := consumer + "/" + eventID
	if f.processed[key] {
		return false, nil
	}
	f.processed[key] = true
	return true, nil
}

func TestAcceptOnceRunsOncePerEvent(t *testing.T) {
	inbox := &fakeInbox{processed: map[string]bool{}}
	calls := 0
	fn := func(context.Context) error { calls++; return nil }

	first, err := AcceptOnce(context.Background(), inbox, inbox, "consumer", "event-1", fn)
	require.NoError(t, err)
	duplicate, err := AcceptOnce(context.Background(), inbox, inbox, "consumer", "event-1", fn)
	require.NoError(t, err)
	other, err := AcceptOnce(context.Background(), inbox, inbox, "other", "event-1", fn)
	require.NoError(t, err)

	assert.True(t, first)
	assert.False(t, duplicate)
	assert.True(t, other)
	assert.Equal(t, 2, calls)
}

func TestAcceptOnceRollsBackInboxRecordOnError(t *testing.T) {
	inbox := &fakeInbox{processed: map[string]bool{}}
	failure := errors.New("relationship write failed")

	accepted, err := AcceptOnce(context.Background(), inbox, inbox, "consumer", "event-1", func(context.Context) error { return failure })
	require.ErrorIs(t, err, failure)
	assert.False(t, accepted)

	accepted, err = AcceptOnce(context.Background(), inbox, inbox, "consumer", "event-1", func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, accepted, "redelivery repeats the rolled back event")

	inbox.acceptErr = errors.New("connection reset")
	_, err = AcceptOnce(context.Background(), inbox, inbox, "consumer", "event-2", func(context.Context) error { return nil })
	require.ErrorIs(t, err, inbox.acceptErr)
	require.ErrorContains(t, err, "accept inbox event")
}
