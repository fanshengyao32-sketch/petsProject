package clock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFixedReturnsUTC(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	value := time.Date(2026, 11, 15, 11, 30, 0, 0, location)
	require.Equal(t, time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC), (Fixed{Time: value}).Now())
}

func TestSystemReturnsCurrentUTC(t *testing.T) {
	before := time.Now().UTC()
	got := (System{}).Now()
	after := time.Now().UTC()
	require.Equal(t, time.UTC, got.Location())
	require.False(t, got.Before(before))
	require.False(t, got.After(after))
}

func TestBusinessDateUsesStoreTimezone(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	value := time.Date(2026, 11, 14, 16, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-11-15", BusinessDate(value, location))
}
