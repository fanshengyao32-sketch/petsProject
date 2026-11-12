package booking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
