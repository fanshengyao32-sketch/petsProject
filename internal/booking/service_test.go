package booking

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestRetryableMySQLErrorsAndCancelledWait(t *testing.T) {
	require.True(t, isRetryableMySQLError(&mysql.MySQLError{Number: 1213}))
	require.True(t, isRetryableMySQLError(&mysql.MySQLError{Number: 1205}))
	require.False(t, isRetryableMySQLError(&mysql.MySQLError{Number: 1062}))
	require.False(t, isRetryableMySQLError(errors.New("other")))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, waitForRetry(ctx, 0))
}

func TestMySQLTransactionRetry(t *testing.T) {
	deadlock := &mysql.MySQLError{Number: 1213, Message: "deadlock details must not escape"}
	attempts := 0
	err := withMySQLTransactionRetry(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return deadlock
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, attempts)

	attempts = 0
	err = withMySQLTransactionRetry(context.Background(), func() error {
		attempts++
		return deadlock
	})
	require.ErrorIs(t, err, ErrTemporarilyBusy)
	require.NotContains(t, err.Error(), "deadlock details")
	require.Equal(t, mysqlTransactionAttempts, attempts)

	sentinel := errors.New("not retryable")
	attempts = 0
	err = withMySQLTransactionRetry(context.Background(), func() error {
		attempts++
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	require.Equal(t, 1, attempts)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = withMySQLTransactionRetry(cancelled, func() error { return deadlock })
	require.ErrorIs(t, err, ErrTemporarilyBusy)
}

type fixedClock struct{ value time.Time }

func (f fixedClock) Now() time.Time { return f.value }

func TestBookingNumberIsStableForSameInput(t *testing.T) {
	now := time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC)
	first := newBookingNo(now, 42, "request-a")
	second := newBookingNo(now, 42, "request-a")
	require.Equal(t, first, second)
	require.Contains(t, first, "BK20261115033000")
}

func TestBookingNumberDiffersForDifferentKeys(t *testing.T) {
	now := time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC)
	require.NotEqual(t, newBookingNo(now, 42, "request-a"), newBookingNo(now, 42, "request-b"))
}
