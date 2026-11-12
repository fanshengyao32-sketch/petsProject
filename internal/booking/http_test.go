package booking

import (
	"bytes"
	"encoding/json"
	"errors"
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

func bookingTestRouter(t *testing.T, service *Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	templates := template.New("")
	for _, name := range []string{"owner.html", "manager.html", "error.html"} {
		_, err := templates.New(name).Parse("{{define \"" + name + "\"}}ok{{.Error}}{{.Success}}{{end}}")
		require.NoError(t, err)
	}
	router.SetHTMLTemplate(templates)
	router.Use(auth.Middleware())
	NewHTTPHandler(service, time.FixedZone("Asia/Shanghai", 8*60*60)).Register(router)
	return router
}

func performRequest(router http.Handler, method, target, session string, body []byte, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if session != "" {
		request.AddCookie(&http.Cookie{Name: "pet_session", Value: session})
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestBookingHTTPWithMySQL(t *testing.T) {
	db := integrationDB(t)
	clearTestBookings(t, db)
	require.NoError(t, db.Exec("DELETE FROM service_slots WHERE id > 3").Error)
	t.Cleanup(func() {
		_ = db.Exec("DELETE FROM bookings").Error
		_ = db.Exec("DELETE FROM service_slots WHERE id > 3").Error
	})
	now := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	service := NewService(db, fixedClock{value: now})
	router := bookingTestRouter(t, service)

	require.Equal(t, http.StatusOK, performRequest(router, http.MethodGet, "/api/slots", "", nil, "").Code)
	require.Equal(t, http.StatusUnauthorized, performRequest(router, http.MethodPost, "/api/bookings", "", []byte(`{}`), "application/json").Code)
	require.Equal(t, http.StatusBadRequest, performRequest(router, http.MethodPost, "/api/bookings", "owner-lin-session", []byte(`{`), "application/json").Code)
	require.Equal(t, http.StatusForbidden, performRequest(router, http.MethodPost, "/api/slots", "owner-lin-session", []byte(`{}`), "application/json").Code)
	require.Equal(t, http.StatusBadRequest, performRequest(router, http.MethodPost, "/api/slots", "manager-xu-session", []byte(`{`), "application/json").Code)

	payload, err := json.Marshal(map[string]any{"pet_id": 1, "slot_id": 1, "request_key": "http-api-create"})
	require.NoError(t, err)
	createdResponse := performRequest(router, http.MethodPost, "/api/bookings", "owner-lin-session", payload, "application/json")
	require.Equal(t, http.StatusCreated, createdResponse.Code)
	var created Booking
	require.NoError(t, json.Unmarshal(createdResponse.Body.Bytes(), &created))
	require.NotEmpty(t, created.BookingNo)
	missingSlotPayload := []byte(`{"pet_id":1,"slot_id":999,"request_key":"http-missing-slot"}`)
	require.Equal(t, http.StatusNotFound, performRequest(router, http.MethodPost, "/api/bookings", "owner-lin-session", missingSlotPayload, "application/json").Code)
	wrongPetPayload := []byte(`{"pet_id":2,"slot_id":1,"request_key":"http-wrong-pet"}`)
	require.Equal(t, http.StatusForbidden, performRequest(router, http.MethodPost, "/api/bookings", "owner-lin-session", wrongPetPayload, "application/json").Code)

	wrongOwner := performRequest(router, http.MethodPost, "/api/bookings/"+created.BookingNo+"/cancel-unpaid", "manager-xu-session", nil, "")
	require.Equal(t, http.StatusForbidden, wrongOwner.Code)
	cancelled := performRequest(router, http.MethodPost, "/api/bookings/"+created.BookingNo+"/cancel-unpaid", "owner-lin-session", nil, "")
	require.Equal(t, http.StatusOK, cancelled.Code)
	require.Equal(t, http.StatusNotFound, performRequest(router, http.MethodPost, "/api/bookings/missing/cancel-unpaid", "owner-lin-session", nil, "").Code)

	require.Equal(t, http.StatusOK, performRequest(router, http.MethodGet, "/owner?success=yes", "owner-lin-session", nil, "").Code)
	require.Equal(t, http.StatusOK, performRequest(router, http.MethodGet, "/manager?error=no", "manager-xu-session", nil, "").Code)

	form := url.Values{"pet_id": {"1"}, "slot_id": {"2"}, "request_key": {"http-form-create"}}
	formResponse := performRequest(router, http.MethodPost, "/owner/bookings", "owner-lin-session", []byte(form.Encode()), "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, formResponse.Code)
	duplicateForm := url.Values{"pet_id": {"1"}, "slot_id": {"2"}, "request_key": {"http-form-duplicate"}}
	duplicateResponse := performRequest(router, http.MethodPost, "/owner/bookings", "owner-lin-session", []byte(duplicateForm.Encode()), "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, duplicateResponse.Code)
	require.Contains(t, duplicateResponse.Header().Get("Location"), "error=")
	duplicateLocation, err := url.QueryUnescape(duplicateResponse.Header().Get("Location"))
	require.NoError(t, err)
	require.Contains(t, duplicateLocation, "该宠物在这个时段已有一笔有效预约，请勿重复预约")
	var formBooking Booking
	require.NoError(t, db.Where("request_key = ?", "http-form-create").First(&formBooking).Error)
	formCancel := performRequest(router, http.MethodPost, "/owner/bookings/"+formBooking.BookingNo+"/cancel", "owner-lin-session", nil, "")
	require.Equal(t, http.StatusFound, formCancel.Code)
	missingCancel := performRequest(router, http.MethodPost, "/owner/bookings/missing/cancel", "owner-lin-session", nil, "")
	require.Contains(t, missingCancel.Header().Get("Location"), "error=")

	badTimeForm := url.Values{"starts_at": {"bad"}, "ends_at": {"bad"}}
	require.Equal(t, http.StatusFound, performRequest(router, http.MethodPost, "/manager/slots", "manager-xu-session", []byte(badTimeForm.Encode()), "application/x-www-form-urlencoded").Code)
	validTimeForm := url.Values{"starts_at": {"2026-11-18T13:00"}, "ends_at": {"2026-11-18T14:00"}}
	require.Equal(t, http.StatusFound, performRequest(router, http.MethodPost, "/manager/slots", "manager-xu-session", []byte(validTimeForm.Encode()), "application/x-www-form-urlencoded").Code)
	overlapResponse := performRequest(router, http.MethodPost, "/manager/slots", "manager-xu-session", []byte(validTimeForm.Encode()), "application/x-www-form-urlencoded")
	require.Contains(t, overlapResponse.Header().Get("Location"), "error=")

	apiSlotPayload := []byte(`{"starts_at":"2026-11-19T05:00:00Z","ends_at":"2026-11-19T06:00:00Z"}`)
	require.Equal(t, http.StatusCreated, performRequest(router, http.MethodPost, "/api/slots", "manager-xu-session", apiSlotPayload, "application/json").Code)
	require.Equal(t, http.StatusBadRequest, performRequest(router, http.MethodPost, "/api/slots", "manager-xu-session", []byte(`{}`), "application/json").Code)

	for index, ownerID := range []uint64{7, 8, 9} {
		_, err := service.CreateBooking(t.Context(), CreateBookingInput{OwnerID: ownerID, PetID: uint64(index + 2), SlotID: 3, RequestKey: "http-fill-" + string(rune('a'+index))})
		require.NoError(t, err)
	}
	fullPayload := []byte(`{"pet_id":1,"slot_id":3,"request_key":"http-capacity-full"}`)
	require.Equal(t, http.StatusConflict, performRequest(router, http.MethodPost, "/api/bookings", "owner-lin-session", fullPayload, "application/json").Code)
}

func TestWriteErrorMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{ErrInvalidRequest, http.StatusBadRequest, "INVALID_REQUEST"},
		{ErrSlotNotFound, http.StatusNotFound, "NOT_FOUND"},
		{ErrCapacityFull, http.StatusConflict, "CAPACITY_FULL"},
		{ErrDuplicateActive, http.StatusConflict, "DUPLICATE_ACTIVE_BOOKING"},
		{ErrSlotStarted, http.StatusConflict, "INVALID_STATE"},
		{ErrPetNotOwned, http.StatusForbidden, "FORBIDDEN"},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		writeError(ctx, test.err)
		require.Equal(t, test.status, response.Code)
		require.True(t, strings.Contains(response.Body.String(), test.code))
		require.NotContains(t, response.Body.String(), test.err.Error())
	}
}

func TestBusinessErrorMessage(t *testing.T) {
	tests := []struct {
		err     error
		message string
	}{
		{ErrSlotNotFound, "预约时段不存在"},
		{ErrSlotStarted, "预约时段已经开始，无法预约"},
		{ErrCapacityFull, "该时段名额已满"},
		{ErrPetNotOwned, "只能为本人名下的宠物预约"},
		{ErrDuplicateActive, "该宠物在这个时段已有一笔有效预约，请勿重复预约"},
		{ErrBookingNotFound, "预约记录不存在"},
		{ErrInvalidBookingState, "当前预约状态不允许执行此操作"},
		{ErrForbidden, "无权执行此操作"},
		{ErrSlotOverlap, "该服务时段与已有时段重叠"},
		{ErrInvalidRequest, "请求参数不正确"},
		{ErrInvalidSlotTime, "结束时间必须晚于开始时间，且开始时间必须在未来"},
		{errors.New("database unavailable"), "系统暂时无法处理该操作，请稍后重试"},
	}
	for _, test := range tests {
		require.Equal(t, test.message, businessErrorMessage(test.err))
	}
}

func TestManagerWithoutStoreAndErrorPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHTTPHandler(nil, time.UTC)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/manager", nil)
	ctx.Set("trusted_identity", auth.Identity{UserID: 2, Role: "manager"})
	handler.managerPage(ctx)
	require.Equal(t, http.StatusForbidden, response.Code)

	templates := template.Must(template.New("").New("error.html").Parse(`{{define "error.html"}}{{.Message}}{{end}}`))
	router := gin.New()
	router.SetHTMLTemplate(templates)
	router.GET("/error", func(c *gin.Context) { handler.renderError(c, ErrCapacityFull) })
	response = performRequest(router, http.MethodGet, "/error", "", nil, "")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "该时段名额已满")
}
