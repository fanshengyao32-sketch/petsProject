package closing

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
)

type HTTPHandler struct {
	service       *Service
	storeLocation *time.Location
}

func NewHTTPHandler(service *Service, storeLocation *time.Location) *HTTPHandler {
	return &HTTPHandler{service: service, storeLocation: storeLocation}
}

func (h *HTTPHandler) Register(router *gin.Engine) {
	router.GET("/manager/no-shows", auth.RequireRole("manager"), h.page)
	router.POST("/manager/bookings/:bookingNo/no-show", auth.RequireRole("manager"), h.markForm)
	router.POST("/api/no-shows/:bookingNo", auth.RequireRole("manager"), h.markAPI)
	router.GET("/manager/daily", auth.RequireRole("manager"), h.dailyPage)
}

func (h *HTTPHandler) dailyPage(c *gin.Context) {
	identity, _ := auth.Current(c)
	if identity.StoreID == nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	dateText := c.Query("date")
	if dateText == "" {
		dateText = h.service.Today(h.storeLocation)
	}
	day, err := time.ParseInLocation("2006-01-02", dateText, h.storeLocation)
	if err != nil {
		c.HTML(http.StatusBadRequest, "daily.html", gin.H{"Identity": identity, "Date": dateText, "Error": "日期格式不正确，请使用年-月-日"})
		return
	}
	report, err := h.service.BuildDailyReport(c.Request.Context(), *identity.StoreID, dateText, day.UTC(), day.AddDate(0, 0, 1).UTC())
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": errorMessage(err)})
		return
	}
	c.HTML(http.StatusOK, "daily.html", gin.H{"Identity": identity, "Date": dateText, "Report": report})
}

func (h *HTTPHandler) page(c *gin.Context) {
	identity, _ := auth.Current(c)
	if identity.StoreID == nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	views, err := h.service.ListNoShows(c.Request.Context(), *identity.StoreID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": errorMessage(err)})
		return
	}
	c.HTML(http.StatusOK, "no-shows.html", gin.H{"Identity": identity, "Bookings": views, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) markForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	_, err := h.service.MarkNoShow(c.Request.Context(), MarkNoShowInput{ManagerID: identity.UserID, BookingNo: c.Param("bookingNo"), Reason: c.PostForm("reason")})
	if err != nil {
		c.Redirect(http.StatusFound, "/manager/no-shows?error="+url.QueryEscape(errorMessage(err)))
		return
	}
	c.Redirect(http.StatusFound, "/manager/no-shows?success="+url.QueryEscape("未到店结果已登记，不自动退款"))
}

func (h *HTTPHandler) markAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	var input struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "未到店原因必填"})
		return
	}
	result, err := h.service.MarkNoShow(c.Request.Context(), MarkNoShowInput{ManagerID: identity.UserID, BookingNo: c.Param("bookingNo"), Reason: input.Reason})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "INTERNAL_ERROR"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	case errors.Is(err, ErrBookingNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, ErrInvalidBookingState), errors.Is(err, ErrSlotNotEnded):
		status, code = http.StatusConflict, "INVALID_STATE"
	}
	c.JSON(status, gin.H{"error": code, "message": errorMessage(err)})
}

func errorMessage(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return "未到店原因必填，且不能超过500字"
	case errors.Is(err, ErrBookingNotFound):
		return "预约记录不存在"
	case errors.Is(err, ErrForbidden):
		return "只能由本店店长操作"
	case errors.Is(err, ErrInvalidBookingState):
		return "只有时段结束后仍为已确认的预约可以标记未到店"
	case errors.Is(err, ErrSlotNotEnded):
		return "服务时段尚未结束，不能标记未到店"
	default:
		return "未到店操作暂时无法处理，请稍后重试"
	}
}
