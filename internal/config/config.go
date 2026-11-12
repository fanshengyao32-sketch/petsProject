package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultStoreTimezone = "Asia/Shanghai"

// Config contains runtime configuration. Secrets are supplied through the
// environment and must never be committed to the repository.
type Config struct {
	Environment           string
	HTTPAddr              string
	MySQLDSN              string
	StoreTimezone         string
	StoreLocation         *time.Location
	TeachingClockEnabled  bool
	TeachingClockNow      *time.Time
	PaymentProvider       string
	MockPaymentBaseURL    string
	PaymentCallbackURL    string
	PaymentCallbackSecret string
}

func Load() (Config, error) {
	locationName := envOrDefault("STORE_TIMEZONE", defaultStoreTimezone)
	location, err := time.LoadLocation(locationName)
	if err != nil {
		return Config{}, fmt.Errorf("load store timezone %q: %w", locationName, err)
	}

	teachingEnabled, err := strconv.ParseBool(envOrDefault("TEACHING_CLOCK_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("parse TEACHING_CLOCK_ENABLED: %w", err)
	}

	var teachingNow *time.Time
	if raw := strings.TrimSpace(os.Getenv("TEACHING_CLOCK_NOW")); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return Config{}, fmt.Errorf("parse TEACHING_CLOCK_NOW: %w", parseErr)
		}
		parsed = parsed.UTC()
		teachingNow = &parsed
	}

	config := Config{
		Environment:           envOrDefault("APP_ENV", "development"),
		HTTPAddr:              envOrDefault("HTTP_ADDR", ":8080"),
		MySQLDSN:              strings.TrimSpace(os.Getenv("MYSQL_DSN")),
		StoreTimezone:         locationName,
		StoreLocation:         location,
		TeachingClockEnabled:  teachingEnabled,
		TeachingClockNow:      teachingNow,
		PaymentProvider:       envOrDefault("PAYMENT_PROVIDER", "mock"),
		MockPaymentBaseURL:    envOrDefault("MOCK_PAYMENT_BASE_URL", "http://localhost:8081"),
		PaymentCallbackURL:    envOrDefault("PAYMENT_CALLBACK_URL", "http://localhost:8080/api/payment/notifications/mock"),
		PaymentCallbackSecret: strings.TrimSpace(os.Getenv("MOCK_PAYMENT_CALLBACK_SECRET")),
	}

	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return errors.New("HTTP_ADDR is required")
	}
	if c.MySQLDSN == "" {
		return errors.New("MYSQL_DSN is required")
	}
	if c.StoreLocation == nil {
		return errors.New("store location is required")
	}
	if c.TeachingClockEnabled && c.TeachingClockNow == nil {
		return errors.New("TEACHING_CLOCK_NOW is required when teaching clock is enabled")
	}
	if c.PaymentProvider != "mock" {
		return fmt.Errorf("unsupported PAYMENT_PROVIDER %q", c.PaymentProvider)
	}
	if c.MockPaymentBaseURL == "" {
		return errors.New("MOCK_PAYMENT_BASE_URL is required")
	}
	if c.PaymentCallbackURL == "" {
		return errors.New("PAYMENT_CALLBACK_URL is required")
	}
	if c.PaymentCallbackSecret == "" {
		return errors.New("MOCK_PAYMENT_CALLBACK_SECRET is required")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
