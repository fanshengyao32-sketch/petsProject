package main

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/fanshengyao32-sketch/petsProject/internal/booking"
	"github.com/fanshengyao32-sketch/petsProject/internal/cancellation"
	"github.com/fanshengyao32-sketch/petsProject/internal/closing"
	"github.com/fanshengyao32-sketch/petsProject/internal/config"
	"github.com/fanshengyao32-sketch/petsProject/internal/fulfillment"
	"github.com/fanshengyao32-sketch/petsProject/internal/payment"
	"github.com/fanshengyao32-sketch/petsProject/internal/platform/clock"
	"github.com/fanshengyao32-sketch/petsProject/internal/platform/database"
	"github.com/fanshengyao32-sketch/petsProject/internal/platform/httpserver"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	appConfig, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.OpenMySQL(ctx, appConfig.MySQLDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	templates, err := loadTemplates("web/templates/*.html", appConfig.StoreLocation)
	if err != nil {
		return err
	}

	var businessClock booking.Clock = clock.System{}
	if appConfig.TeachingClockEnabled {
		businessClock = clock.Fixed{Time: *appConfig.TeachingClockNow}
	}
	application := newApplication(db, appConfig, businessClock, templates)
	expirationDone := startExpirationWorker(ctx, application.bookingService, 30*time.Second)
	server := &http.Server{
		Addr:              appConfig.HTTPAddr,
		Handler:           application.router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	slog.Info("server started", "addr", appConfig.HTTPAddr, "store_timezone", appConfig.StoreTimezone)
	return serve(ctx, server, expirationDone)
}

type application struct {
	router         *gin.Engine
	bookingService *booking.Service
}

func loadTemplates(pattern string, storeLocation *time.Location) (*template.Template, error) {
	return template.New("").Funcs(templateFunctions(storeLocation)).ParseGlob(pattern)
}

func templateFunctions(storeLocation *time.Location) template.FuncMap {
	return template.FuncMap{
		"divideCents": func(value int64) float64 { return float64(value) / 100 },
		"storeTime":   func(value time.Time) string { return value.In(storeLocation).Format("2006-01-02 15:04") },
		"statusText": func(value string) string {
			labels := map[string]string{
				"pending_payment": "待付款", "expired": "占位已超时", "closed": "已取消", "confirmed": "已确认",
				"cancel_pending": "取消待审核", "checked_in": "已核销", "in_service": "服务中", "completed": "已完成",
				"cancelled": "已取消并待退款", "no_show": "未到店",
			}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
		"paymentStatusText": func(value string) string {
			labels := map[string]string{"pending": "支付处理中", "unknown": "支付待确认", "succeeded": "支付成功", "failed": "支付失败"}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
		"refundStatusText": func(value string) string {
			labels := map[string]string{"pending": "待退款", "unknown": "退款待确认", "processing": "退款处理中", "succeeded": "退款成功", "failed": "退款失败"}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
		"refundStatusesText": func(value string) string {
			labels := map[string]string{"pending": "待退款", "unknown": "退款待确认", "processing": "退款处理中", "succeeded": "退款成功", "failed": "退款失败"}
			parts := strings.Split(value, ",")
			for index, part := range parts {
				if label, ok := labels[part]; ok {
					parts[index] = label
				}
			}
			return strings.Join(parts, "、")
		},
		"refundReasonText": func(value string) string {
			labels := map[string]string{"late_payment": "预约关闭后的晚到支付", "duplicate_payment": "重复扣款", "approved_cancellation": "取消预约退款"}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
		"paymentFailureText": func(value string) string {
			labels := map[string]string{"MOCK_DECLINED": "模拟渠道拒绝", "MOCK_TIMEOUT": "模拟渠道超时", "PROVIDER_UNAVAILABLE": "支付渠道暂不可用", "DECLINED": "支付被拒绝", "TIMEOUT": "渠道超时"}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
		"refundFailureText": func(value string) string {
			labels := map[string]string{"MOCK_REFUND_REJECTED": "模拟渠道拒绝退款", "MOCK_REFUND_TIMEOUT": "模拟渠道退款超时", "PROVIDER_UNAVAILABLE": "退款渠道暂不可用"}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
		"cancellationStatusText": func(value string) string {
			labels := map[string]string{"pending": "待客服审核", "approved": "客服已批准", "rejected": "客服已拒绝", "auto_approved": "提前取消已自动批准"}
			if label, ok := labels[value]; ok {
				return label
			}
			return value
		},
	}
}

func newApplication(db *database.MySQL, appConfig config.Config, businessClock booking.Clock, templates *template.Template) application {
	router := httpserver.NewRouter(httpserver.Dependencies{
		DB:            db.SQL,
		StoreTimezone: appConfig.StoreTimezone,
		Template:      templates,
	})
	router.Use(auth.Middleware())
	router.GET("/demo/login/:token", auth.DemoLogin)
	bookingService := booking.NewService(db.GORM, businessClock)
	booking.NewHTTPHandler(bookingService, appConfig.StoreLocation).Register(router)
	mockPayment := payment.NewMockClient(appConfig.MockPaymentBaseURL, appConfig.PaymentCallbackURL, appConfig.PaymentCallbackSecret, nil)
	paymentService := payment.NewService(db.GORM, mockPayment, businessClock)
	payment.NewHTTPHandler(paymentService, mockPayment, mockPayment).Register(router)
	cancellationService := cancellation.NewService(db.GORM, businessClock)
	cancellation.NewHTTPHandler(cancellationService).Register(router)
	fulfillmentService := fulfillment.NewService(db.GORM, businessClock)
	fulfillment.NewHTTPHandler(fulfillmentService).Register(router)
	closingService := closing.NewService(db.GORM, businessClock)
	closing.NewHTTPHandler(closingService, appConfig.StoreLocation).Register(router)
	return application{router: router, bookingService: bookingService}
}

func startExpirationWorker(ctx context.Context, bookingService *booking.Service, interval time.Duration) <-chan struct{} {
	expirationDone := make(chan struct{})
	go func() {
		defer close(expirationDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				expireCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := bookingService.ExpireHolds(expireCtx); err != nil {
					slog.Error("expire booking holds", "error", err)
				}
				cancel()
			}
		}
	}()
	return expirationDone
}

func serve(ctx context.Context, server *http.Server, expirationDone <-chan struct{}) error {
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		<-expirationDone
		return err
	}
}
