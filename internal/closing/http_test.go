package closing

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

func closingRouter(t *testing.T, service *Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	templates := template.New("")
	for _, name := range []string{"no-shows.html", "daily.html", "error.html"} {
		_, err := templates.New(name).Parse("{{define \"" + name + "\"}}ok{{.Error}}{{.Success}}{{end}}")
		require.NoError(t, err)
	}
	router.SetHTMLTemplate(templates)
	router.Use(auth.Middleware())
	NewHTTPHandler(service, time.UTC).Register(router)
	return router
}

func closingRequest(router http.Handler, method, target, session string, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if session != "" {
		request.AddCookie(&http.Cookie{Name: "pet_session_manager", Value: session})
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestNoShowHTTPFlow(t *testing.T) {
	db := closingDB(t)
	clearClosing(t, db)
	t.Cleanup(func() { clearClosing(t, db) })
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})
	router := closingRouter(t, service)
	slotID := insertClosingSlot(t, db, now.Add(-2*time.Hour), now.Add(-time.Hour), 1)
	insertClosingBooking(t, db, "BK-NO-SHOW-HTTP", "confirmed", slotID)

	require.Equal(t, http.StatusFound, closingRequest(router, http.MethodGet, "/manager/no-shows", "", nil, "").Code)
	require.Equal(t, http.StatusOK, closingRequest(router, http.MethodGet, "/manager/no-shows", "manager-xu-session", nil, "").Code)
	require.Equal(t, http.StatusBadRequest, closingRequest(router, http.MethodPost, "/api/no-shows/BK-NO-SHOW-HTTP", "manager-xu-session", []byte(`{`), "application/json").Code)
	bad := closingRequest(router, http.MethodPost, "/api/no-shows/BK-NO-SHOW-HTTP", "manager-xu-session", []byte(`{"reason":""}`), "application/json")
	require.Equal(t, http.StatusBadRequest, bad.Code)
	form := url.Values{"reason": {"用户未出现，电话未接"}}
	response := closingRequest(router, http.MethodPost, "/manager/bookings/BK-NO-SHOW-HTTP/no-show", "manager-xu-session", []byte(form.Encode()), "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, response.Code)
	require.Contains(t, response.Header().Get("Location"), "success=")
	replayed := closingRequest(router, http.MethodPost, "/api/no-shows/BK-NO-SHOW-HTTP", "manager-xu-session", []byte(`{"reason":"重复"}`), "application/json")
	require.Equal(t, http.StatusOK, replayed.Code)
	missing := closingRequest(router, http.MethodPost, "/api/no-shows/missing", "manager-xu-session", []byte(`{"reason":"未到店"}`), "application/json")
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func TestNoShowErrorMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		err    error
		status int
		text   string
	}{
		{ErrInvalidRequest, http.StatusBadRequest, "原因必填"},
		{ErrBookingNotFound, http.StatusNotFound, "不存在"},
		{ErrForbidden, http.StatusForbidden, "本店店长"},
		{ErrInvalidBookingState, http.StatusConflict, "已确认"},
		{ErrSlotNotEnded, http.StatusConflict, "尚未结束"},
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		writeError(ctx, test.err)
		require.Equal(t, test.status, response.Code)
		require.True(t, strings.Contains(response.Body.String(), test.text))
	}
}

func TestDailyHTTPPageAndDateValidation(t *testing.T) {
	db := closingDB(t)
	clearClosing(t, db)
	t.Cleanup(func() { clearClosing(t, db) })
	now := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	router := closingRouter(t, NewService(db, fixedClock{value: now}))
	require.Equal(t, http.StatusFound, closingRequest(router, http.MethodGet, "/manager/daily", "", nil, "").Code)
	require.Equal(t, http.StatusOK, closingRequest(router, http.MethodGet, "/manager/daily?date=2026-11-12", "manager-xu-session", nil, "").Code)
	bad := closingRequest(router, http.MethodGet, "/manager/daily?date=bad", "manager-xu-session", nil, "")
	require.Equal(t, http.StatusBadRequest, bad.Code)
	require.Contains(t, bad.Body.String(), "日期格式不正确")
}
