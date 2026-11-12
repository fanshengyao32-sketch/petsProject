package fulfillment

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func fulfillmentRouter(t *testing.T, service *Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	templates := template.New("")
	for _, name := range []string{"front-desk.html", "service-workbench.html", "error.html"} {
		_, err := templates.New(name).Parse("{{define \"" + name + "\"}}ok{{.Error}}{{.Success}}{{end}}")
		require.NoError(t, err)
	}
	router.SetHTMLTemplate(templates)
	router.Use(auth.Middleware())
	NewHTTPHandler(service).Register(router)
	return router
}

func fulfillmentRequest(router http.Handler, method, target, role string, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	tokens := map[string]string{"front_desk": "front-zhou-session", "service_staff": "service-session", "front_other": "front-other-session"}
	if token := tokens[role]; token != "" {
		cookieRole := role
		if role == "front_other" {
			cookieRole = "front_desk"
		}
		request.AddCookie(&http.Cookie{Name: "pet_session_" + cookieRole, Value: token})
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestFulfillmentHTTPRealFlow(t *testing.T) {
	db := fulfillmentDB(t)
	clearFulfillment(t, db)
	t.Cleanup(func() { clearFulfillment(t, db) })
	service := NewService(db, fixedClock{value: time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)})
	router := fulfillmentRouter(t, service)
	insertFulfillmentBooking(t, db, "BK-HTTP-FLOW", "confirmed", 1)

	require.Equal(t, http.StatusFound, fulfillmentRequest(router, http.MethodGet, "/front-desk", "", nil, "").Code)
	require.Equal(t, http.StatusOK, fulfillmentRequest(router, http.MethodGet, "/front-desk", "front_desk", nil, "").Code)
	require.Equal(t, http.StatusOK, fulfillmentRequest(router, http.MethodGet, "/service-workbench", "service_staff", nil, "").Code)
	require.Equal(t, http.StatusBadRequest, fulfillmentRequest(router, http.MethodPost, "/api/fulfillment/BK-HTTP-FLOW/check-in", "front_desk", []byte(`{`), "application/json").Code)

	checkin := url.Values{"idempotency_key": {"http-checkin"}}
	response := fulfillmentRequest(router, http.MethodPost, "/front-desk/bookings/BK-HTTP-FLOW/check-in", "front_desk", []byte(checkin.Encode()), "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, response.Code)
	require.Contains(t, response.Header().Get("Location"), "success=")
	start := fulfillmentRequest(router, http.MethodPost, "/api/fulfillment/BK-HTTP-FLOW/start", "service_staff", []byte(`{"idempotency_key":"http-start"}`), "application/json")
	require.Equal(t, http.StatusOK, start.Code)
	complete := url.Values{"idempotency_key": {"http-complete"}}
	response = fulfillmentRequest(router, http.MethodPost, "/service-workbench/bookings/BK-HTTP-FLOW/complete", "service_staff", []byte(complete.Encode()), "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, response.Code)

	badOrder := fulfillmentRequest(router, http.MethodPost, "/api/fulfillment/BK-HTTP-FLOW/start", "service_staff", []byte(`{"idempotency_key":"again"}`), "application/json")
	require.Equal(t, http.StatusOK, badOrder.Code)
	missing := fulfillmentRequest(router, http.MethodPost, "/api/fulfillment/missing/check-in", "front_desk", []byte(`{"idempotency_key":"missing"}`), "application/json")
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func TestFulfillmentErrorMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		err    error
		status int
		text   string
	}{
		{ErrInvalidRequest, http.StatusBadRequest, "参数不正确"},
		{ErrBookingNotFound, http.StatusNotFound, "不存在"},
		{ErrForbidden, http.StatusForbidden, "本店"},
		{ErrInvalidBookingState, http.StatusConflict, "状态"},
		{ErrCancellationPending, http.StatusConflict, "取消审核"},
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		writeError(ctx, test.err)
		require.Equal(t, test.status, response.Code)
		require.True(t, strings.Contains(response.Body.String(), test.text))
	}
}
