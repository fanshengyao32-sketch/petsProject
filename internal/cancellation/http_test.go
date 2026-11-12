package cancellation

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func cancellationRouter(t *testing.T, service *Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	templates := template.New("")
	for _, name := range []string{"support.html", "error.html"} {
		_, err := templates.New(name).Parse("{{define \"" + name + "\"}}ok{{.Error}}{{.Success}}{{end}}")
		require.NoError(t, err)
	}
	router.SetHTMLTemplate(templates)
	router.Use(auth.Middleware())
	NewHTTPHandler(service).Register(router)
	return router
}

func cancellationHTTP(router http.Handler, method, target, session string, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if session != "" {
		request.AddCookie(&http.Cookie{Name: "pet_session_" + session, Value: map[string]string{"owner": "owner-lin-session", "support": "support-gu-session"}[session]})
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestCancellationHTTPPendingAndReview(t *testing.T) {
	db := cancellationDB(t)
	clearCancellationData(t, db)
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	insertSlot(t, db, 30, now.Add(time.Hour))
	insertConfirmed(t, db, "BK-HTTP-CANCEL", 1, 1, 30)
	service := NewService(db, fixedClock{value: now})
	router := cancellationRouter(t, service)

	require.Equal(t, http.StatusFound, cancellationHTTP(router, http.MethodGet, "/support", "", nil, "").Code)
	require.Equal(t, http.StatusFound, cancellationHTTP(router, http.MethodGet, "/support", "owner", nil, "").Code)
	require.Equal(t, http.StatusBadRequest, cancellationHTTP(router, http.MethodPost, "/api/cancellations", "owner", []byte(`{`), "application/json").Code)

	requestBody := []byte(`{"booking_no":"BK-HTTP-CANCEL","reason":"临时无法到店"}`)
	response := cancellationHTTP(router, http.MethodPost, "/api/cancellations", "owner", requestBody, "application/json")
	require.Equal(t, http.StatusOK, response.Code)
	var pending Request
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &pending))
	require.Equal(t, StatusPending, pending.Status)
	require.Equal(t, http.StatusOK, cancellationHTTP(router, http.MethodGet, "/support", "support", nil, "").Code)
	require.Equal(t, http.StatusBadRequest, cancellationHTTP(router, http.MethodPost, "/api/cancellations/"+strconv.FormatUint(pending.ID, 10)+"/review", "support", []byte(`{`), "application/json").Code)

	reviewBody := []byte(`{"decision":"approved","review_reason":"情况属实"}`)
	reviewResponse := cancellationHTTP(router, http.MethodPost, "/api/cancellations/"+strconv.FormatUint(pending.ID, 10)+"/review", "support", reviewBody, "application/json")
	require.Equal(t, http.StatusOK, reviewResponse.Code)
}

func TestCancellationFormsAndErrors(t *testing.T) {
	db := cancellationDB(t)
	clearCancellationData(t, db)
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	insertSlot(t, db, 31, now.Add(time.Hour))
	insertConfirmed(t, db, "BK-FORM-CANCEL", 1, 1, 31)
	service := NewService(db, fixedClock{value: now})
	router := cancellationRouter(t, service)

	badForm := url.Values{"reason": {""}}
	badResponse := cancellationHTTP(router, http.MethodPost, "/owner/bookings/BK-FORM-CANCEL/cancel-confirmed", "owner", []byte(badForm.Encode()), "application/x-www-form-urlencoded")
	require.Contains(t, badResponse.Header().Get("Location"), "error=")
	badLocation, err := url.QueryUnescape(badResponse.Header().Get("Location"))
	require.NoError(t, err)
	require.Contains(t, badLocation, "距离开始不足2小时，请填写取消原因")
	form := url.Values{"reason": {"宠物不适"}}
	response := cancellationHTTP(router, http.MethodPost, "/owner/bookings/BK-FORM-CANCEL/cancel-confirmed", "owner", []byte(form.Encode()), "application/x-www-form-urlencoded")
	require.Contains(t, response.Header().Get("Location"), "success=")
	var pending Request
	require.NoError(t, db.Where("booking_id = (SELECT id FROM bookings WHERE booking_no = ?)", "BK-FORM-CANCEL").First(&pending).Error)
	review := url.Values{"decision": {"rejected"}, "review_reason": {"无法批准"}}
	reviewResponse := cancellationHTTP(router, http.MethodPost, "/support/cancellations/"+strconv.FormatUint(pending.ID, 10)+"/review", "support", []byte(review.Encode()), "application/x-www-form-urlencoded")
	require.Contains(t, reviewResponse.Header().Get("Location"), "success=")
	missingResponse := cancellationHTTP(router, http.MethodPost, "/support/cancellations/999999/review", "support", []byte(review.Encode()), "application/x-www-form-urlencoded")
	require.Contains(t, missingResponse.Header().Get("Location"), "error=")

	insertSlot(t, db, 32, now.Add(3*time.Hour))
	insertConfirmed(t, db, "BK-FORM-AUTO", 1, 1, 32)
	autoResponse := cancellationHTTP(router, http.MethodPost, "/owner/bookings/BK-FORM-AUTO/cancel-confirmed", "owner", []byte(url.Values{}.Encode()), "application/x-www-form-urlencoded")
	require.Contains(t, autoResponse.Header().Get("Location"), "success=")
}

func TestCancellationErrorMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		err    error
		status int
	}{
		{ErrInvalidRequest, http.StatusBadRequest}, {ErrBookingNotFound, http.StatusNotFound},
		{ErrForbidden, http.StatusForbidden}, {ErrInvalidBookingState, http.StatusConflict},
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		writeError(ctx, test.err)
		require.Equal(t, test.status, response.Code)
		require.Contains(t, response.Body.String(), cancellationErrorMessage(test.err))
		require.NotContains(t, response.Body.String(), test.err.Error())
	}
	for _, test := range []struct {
		err     error
		message string
	}{
		{ErrInvalidRequest, "取消申请参数不正确"},
		{ErrBookingNotFound, "预约记录不存在"},
		{ErrRequestNotFound, "取消申请不存在"},
		{ErrForbidden, "无权处理该取消申请"},
		{ErrInvalidBookingState, "当前预约状态不允许取消"},
		{ErrServiceStarted, "预约时段已经开始，不能再申请取消退款"},
		{ErrReasonRequired, "距离开始不足2小时，请填写取消原因"},
		{ErrPaymentNotFound, "未找到可退款的成功支付记录"},
		{ErrAlreadyReviewed, "该取消申请已经审核，请勿重复操作"},
		{errors.New("database unavailable"), "系统暂时无法处理取消申请，请稍后重试"},
	} {
		require.Equal(t, test.message, cancellationErrorMessage(test.err))
	}
}
