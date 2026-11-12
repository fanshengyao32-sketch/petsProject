package main

import (
	"context"
	"html/template"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/booking"
	"github.com/fanshengyao32-sketch/petsProject/internal/config"
	"github.com/fanshengyao32-sketch/petsProject/internal/platform/clock"
	"github.com/fanshengyao32-sketch/petsProject/internal/platform/database"
	"github.com/stretchr/testify/require"
)

func TestTemplateFunctions(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	functions := templateFunctions(location)
	require.Equal(t, 99.0, functions["divideCents"].(func(int64) float64)(9900))
	require.Equal(t, "2026-11-12 11:30", functions["storeTime"].(func(time.Time) string)(time.Date(2026, 11, 12, 3, 30, 0, 0, time.UTC)))

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"status", "completed", "已完成"},
		{"payment", "succeeded", "支付成功"},
		{"refund", "failed", "退款失败"},
		{"refund statuses", "pending,failed", "待退款、退款失败"},
		{"refund reason", "approved_cancellation", "取消预约退款"},
		{"payment failure", "MOCK_DECLINED", "模拟渠道拒绝"},
		{"refund failure", "MOCK_REFUND_TIMEOUT", "模拟渠道退款超时"},
		{"cancellation", "auto_approved", "提前取消已自动批准"},
	}
	functionNames := map[string]string{
		"status":          "statusText",
		"payment":         "paymentStatusText",
		"refund":          "refundStatusText",
		"refund statuses": "refundStatusesText",
		"refund reason":   "refundReasonText",
		"payment failure": "paymentFailureText",
		"refund failure":  "refundFailureText",
		"cancellation":    "cancellationStatusText",
	}
	for _, test := range tests {
		function := functions[functionNames[test.name]].(func(string) string)
		require.Equal(t, test.expected, function(test.input))
		require.Equal(t, "unmapped", function("unmapped"))
	}
}

func TestLoadTemplates(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "page.html")
	require.NoError(t, os.WriteFile(file, []byte(`{{ define "page" }}{{ divideCents 9900 }}{{ end }}`), 0o600))
	parsed, err := loadTemplates(filepath.Join(directory, "*.html"), time.UTC)
	require.NoError(t, err)
	require.NotNil(t, parsed.Lookup("page"))

	_, err = loadTemplates(filepath.Join(directory, "missing", "*.html"), time.UTC)
	require.Error(t, err)
}

func TestApplicationAssemblyAndExpirationWorker(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), dsn)
	require.NoError(t, err)
	defer db.Close()

	appConfig := config.Config{
		StoreTimezone:         "Asia/Shanghai",
		StoreLocation:         time.UTC,
		MockPaymentBaseURL:    "http://127.0.0.1:8081",
		PaymentCallbackURL:    "http://127.0.0.1:8080/api/payment/notifications/mock",
		PaymentCallbackSecret: "secret",
	}
	application := newApplication(db, appConfig, clock.Fixed{Time: time.Now().UTC()}, template.New("test"))
	require.NotNil(t, application.bookingService)

	response := httptest.NewRecorder()
	application.router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	require.Equal(t, http.StatusOK, response.Code)

	ctx, cancel := context.WithCancel(context.Background())
	done := startExpirationWorker(ctx, application.bookingService, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		cancel()
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestExpirationWorkerProcessesTick(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), dsn)
	require.NoError(t, err)
	defer db.Close()
	service := booking.NewService(db.GORM, clock.Fixed{Time: time.Now().UTC()})
	ctx, cancel := context.WithCancel(context.Background())
	done := startExpirationWorker(ctx, service, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expiration worker did not stop")
	}
}

func TestServeShutdownAndListenErrors(t *testing.T) {
	done := make(chan struct{})
	close(done)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	server := &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}
	require.NoError(t, serve(ctx, server, done))

	closedServer := &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}
	require.NoError(t, closedServer.Shutdown(context.Background()))
	require.NoError(t, serve(context.Background(), closedServer, done))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	occupied := &http.Server{Addr: listener.Addr().String(), Handler: http.NewServeMux()}
	require.Error(t, serve(context.Background(), occupied, done))
}

func TestRunRejectsInvalidConfigurationAndDatabase(t *testing.T) {
	t.Setenv("MYSQL_DSN", "")
	t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "")
	require.ErrorContains(t, run(), "MYSQL_DSN")

	t.Setenv("MYSQL_DSN", "invalid dsn")
	t.Setenv("MOCK_PAYMENT_CALLBACK_SECRET", "secret")
	require.ErrorContains(t, run(), "open mysql")
}
