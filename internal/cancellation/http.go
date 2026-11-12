package cancellation

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) *HTTPHandler { return &HTTPHandler{service: service} }

func (h *HTTPHandler) Register(router *gin.Engine) {
	router.POST("/owner/bookings/:bookingNo/cancel-confirmed", auth.RequireRole("owner"), h.requestForm)
	router.POST("/api/cancellations", auth.RequireRole("owner"), h.requestAPI)
	router.GET("/support", auth.RequireRole("support"), h.supportPage)
	router.POST("/support/cancellations/:id/review", auth.RequireRole("support"), h.reviewForm)
	router.POST("/api/cancellations/:id/review", auth.RequireRole("support"), h.reviewAPI)
}

func (h *HTTPHandler) requestForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	result, err := h.service.Request(c.Request.Context(), identity.UserID, c.Param("bookingNo"), c.PostForm("reason"))
	if err != nil {
		c.Redirect(http.StatusFound, "/owner?error="+url.QueryEscape(cancellationErrorMessage(err)))
		return
	}
	message := "取消申请已提交，等待客服审核"
	if result.Status == StatusAutoApproved {
		message = "取消已自动批准，名额已释放，退款进入待处理"
	}
	c.Redirect(http.StatusFound, "/owner?success="+url.QueryEscape(message))
}

func (h *HTTPHandler) requestAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	var input struct {
		BookingNo string `json:"booking_no"`
		Reason    string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	result, err := h.service.Request(c.Request.Context(), identity.UserID, input.BookingNo, input.Reason)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *HTTPHandler) supportPage(c *gin.Context) {
	identity, _ := auth.Current(c)
	views, err := h.service.List(c.Request.Context())
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": cancellationErrorMessage(err)})
		return
	}
	c.HTML(http.StatusOK, "support.html", gin.H{"Identity": identity, "Cancellations": views, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) reviewForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	requestID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	_, err := h.service.Review(c.Request.Context(), identity.UserID, requestID, c.PostForm("decision"), c.PostForm("review_reason"))
	if err != nil {
		c.Redirect(http.StatusFound, "/support?error="+url.QueryEscape(cancellationErrorMessage(err)))
		return
	}
	c.Redirect(http.StatusFound, "/support?success="+url.QueryEscape("审核结果已保存"))
}

func (h *HTTPHandler) reviewAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	requestID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var input struct {
		Decision     string `json:"decision"`
		ReviewReason string `json:"review_reason"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	result, err := h.service.Review(c.Request.Context(), identity.UserID, requestID, input.Decision, input.ReviewReason)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "INTERNAL_ERROR"
	switch {
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrReasonRequired):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	case errors.Is(err, ErrBookingNotFound), errors.Is(err, ErrRequestNotFound), errors.Is(err, ErrPaymentNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, ErrInvalidBookingState), errors.Is(err, ErrServiceStarted), errors.Is(err, ErrAlreadyReviewed):
		status, code = http.StatusConflict, "INVALID_STATE"
	}
	c.JSON(status, gin.H{"error": code, "message": cancellationErrorMessage(err)})
}

func cancellationErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return "取消申请参数不正确"
	case errors.Is(err, ErrBookingNotFound):
		return "预约记录不存在"
	case errors.Is(err, ErrRequestNotFound):
		return "取消申请不存在"
	case errors.Is(err, ErrForbidden):
		return "无权处理该取消申请"
	case errors.Is(err, ErrInvalidBookingState):
		return "当前预约状态不允许取消"
	case errors.Is(err, ErrServiceStarted):
		return "预约时段已经开始，不能再申请取消退款"
	case errors.Is(err, ErrReasonRequired):
		return "距离开始不足2小时，请填写取消原因"
	case errors.Is(err, ErrPaymentNotFound):
		return "未找到可退款的成功支付记录"
	case errors.Is(err, ErrAlreadyReviewed):
		return "该取消申请已经审核，请勿重复操作"
	default:
		return "系统暂时无法处理取消申请，请稍后重试"
	}
}
