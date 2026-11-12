package config

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:pass@tcp(localhost:3306)/pet_booking?parseTime=true&loc=UTC")
	t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "test-secret")
	t.Setenv("TEACHING_CLOCK_ENABLED", "true")
	t.Setenv("TEACHING_CLOCK_NOW", "2026-11-15T03:30:00Z")

	got, err := Load()
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", got.StoreTimezone)
	require.True(t, got.TeachingClockEnabled)
	require.Equal(t, time.Date(2026, 11, 15, 3, 30, 0, 0, time.UTC), *got.TeachingClockNow)
}

func TestLoadRejectsTeachingClockWithoutTime(t *testing.T) {
	t.Setenv("MYSQL_DSN", "dsn")
	t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "test-secret")
	t.Setenv("TEACHING_CLOCK_ENABLED", "true")
	t.Setenv("TEACHING_CLOCK_NOW", "")

	_, err := Load()
	require.ErrorContains(t, err, "TEACHING_CLOCK_NOW")
}

func TestValidateRejectsUnsupportedProvider(t *testing.T) {
	config := validConfig()
	config.PaymentProvider = "alipay"
	require.ErrorContains(t, config.Validate(), "unsupported")
}

func TestValidateRequiredValues(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*Config)
		message string
	}{
		{"http address", func(c *Config) { c.HTTPAddr = "" }, "HTTP_ADDR"},
		{"mysql dsn", func(c *Config) { c.MySQLDSN = "" }, "MYSQL_DSN"},
		{"store location", func(c *Config) { c.StoreLocation = nil }, "store location"},
		{"teaching time", func(c *Config) { c.TeachingClockEnabled, c.TeachingClockNow = true, nil }, "TEACHING_CLOCK_NOW"},
		{"mock base url", func(c *Config) { c.MockPaymentBaseURL = "" }, "MOCK_PAYMENT_BASE_URL"},
		{"callback url", func(c *Config) { c.PaymentCallbackURL = "" }, "PAYMENT_CALLBACK_URL"},
		{"callback secret", func(c *Config) { c.PaymentCallbackSecret = "" }, "MOCK_PAYMENT_CALLBACK_SECRET"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig()
			test.change(&config)
			require.ErrorContains(t, config.Validate(), test.message)
		})
	}
	require.NoError(t, validConfig().Validate())
}

func TestLoadRejectsInvalidEnvironmentValues(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		message string
	}{
		{"timezone", "STORE_TIMEZONE", "Not/A-Timezone", "load store timezone"},
		{"teaching enabled", "TEACHING_CLOCK_ENABLED", "perhaps", "parse TEACHING_CLOCK_ENABLED"},
		{"teaching time", "TEACHING_CLOCK_NOW", "not-a-time", "parse TEACHING_CLOCK_NOW"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MYSQL_DSN", "dsn")
			t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "secret")
			t.Setenv(test.key, test.value)
			_, err := Load()
			require.ErrorContains(t, err, test.message)
		})
	}
}

func TestLoadTrimsValuesAndUsesDefaults(t *testing.T) {
	t.Setenv("MYSQL_DSN", "  dsn  ")
	t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "  secret  ")
	t.Setenv("HTTP_ADDR", "   ")
	config, err := Load()
	require.NoError(t, err)
	require.Equal(t, "dsn", config.MySQLDSN)
	require.Equal(t, "secret", config.PaymentCallbackSecret)
	require.Equal(t, ":8080", config.HTTPAddr)
	require.Equal(t, "development", config.Environment)
	require.False(t, config.TeachingClockEnabled)
	require.Nil(t, config.TeachingClockNow)
	require.True(t, strings.HasPrefix(config.MockPaymentBaseURL, "http"))
}

func validConfig() Config {
	now := time.Now().UTC()
	return Config{
		HTTPAddr:              ":8080",
		MySQLDSN:              "dsn",
		StoreLocation:         time.UTC,
		TeachingClockNow:      &now,
		PaymentProvider:       "mock",
		MockPaymentBaseURL:    "http://localhost:8081",
		PaymentCallbackURL:    "http://localhost:8080/callback",
		PaymentCallbackSecret: "secret",
	}
}
