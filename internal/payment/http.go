package payment

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
)

type HTTPHandler struct {
	service   *Service
	provider  Provider
	simulator SimulationController
}

func NewHTTPHandler(service *Service, provider Provider, simulator SimulationController) *HTTPHandler {
	return &HTTPHandler{service: service, provider: provider, simulator: simulator}
}

func (h *HTTPHandler) Register(router *gin.Engine) {
	router.GET("/owner/payments", auth.RequireRole("owner"), h.ownerPage)
	router.POST("/owner/bookings/:bookingNo/pay", auth.RequireRole("owner"), h.startForm)
	router.POST("/owner/payments/:requestNo/late-success", auth.RequireRole("owner"), h.lateSuccessForm)
	router.POST("/api/payments", auth.RequireRole("owner"), h.startAPI)
	router.GET("/api/payments", auth.RequireRole("owner"), h.listAPI)
	router.POST("/api/payment/notifications/mock", h.notificationCallback)
	router.GET("/support/refunds", auth.RequireRole("support"), h.refundPage)
	router.POST("/support/refunds/:refundNo/execute", auth.RequireRole("support"), h.executeRefundForm)
	router.POST("/api/refunds/:refundNo/execute", auth.RequireRole("support"), h.executeRefundAPI)
}

func (h *HTTPHandler) refundPage(c *gin.Context) {
	identity, _ := auth.Current(c)
	views, err := h.service.ListRefunds(c.Request.Context())
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": paymentErrorMessage(err)})
		return
	}
	c.HTML(http.StatusOK, "refunds.html", gin.H{"Identity": identity, "Refunds": views, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) executeRefundForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	result, err := h.service.ExecuteRefund(c.Request.Context(), ExecuteRefundInput{ActorID: identity.UserID, RefundNo: c.Param("refundNo"), Scenario: c.PostForm("scenario")})
	if err != nil {
		c.Redirect(http.StatusFound, "/support/refunds?error="+url.QueryEscape(paymentErrorMessage(err)))
		return
	}
	message := "退款请求已处理，当前状态：" + refundStatusMessage(result.Status)
	c.Redirect(http.StatusFound, "/support/refunds?success="+url.QueryEscape(message))
}

func (h *HTTPHandler) executeRefundAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	var input struct {
		Scenario string `json:"scenario"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "退款请求参数不正确"})
		return
	}
	result, err := h.service.ExecuteRefund(c.Request.Context(), ExecuteRefundInput{ActorID: identity.UserID, RefundNo: c.Param("refundNo"), Scenario: input.Scenario})
	if err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *HTTPHandler) ownerPage(c *gin.Context) {
	identity, _ := auth.Current(c)
	views, err := h.service.ListOwner(c.Request.Context(), identity.UserID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": paymentErrorMessage(err)})
		return
	}
	c.HTML(http.StatusOK, "payments.html", gin.H{"Identity": identity, "Payments": views, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) startForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	_, err := h.service.Start(c.Request.Context(), StartInput{OwnerID: identity.UserID, BookingNo: c.Param("bookingNo"), RequestNo: c.PostForm("request_no"), Scenario: c.PostForm("scenario")})
	if err != nil {
		c.Redirect(http.StatusFound, "/owner/payments?error="+url.QueryEscape(paymentErrorMessage(err)))
		return
	}
	c.Redirect(http.StatusFound, "/owner/payments?success="+url.QueryEscape("支付渠道结果已处理"))
}

func (h *HTTPHandler) lateSuccessForm(c *gin.Context) {
	if h.simulator == nil {
		c.Redirect(http.StatusFound, "/owner/payments?error="+url.QueryEscape("当前支付渠道不支持模拟通知"))
		return
	}
	identity, _ := auth.Current(c)
	if err := h.service.RequireOwnerPayment(c.Request.Context(), identity.UserID, c.Param("requestNo")); err != nil {
		c.Redirect(http.StatusFound, "/owner/payments?error="+url.QueryEscape(paymentErrorMessage(err)))
		return
	}
	if err := h.simulator.TriggerPaymentNotification(c.Request.Context(), c.Param("requestNo"), true); err != nil {
		c.Redirect(http.StatusFound, "/owner/payments?error="+url.QueryEscape(paymentErrorMessage(err)))
		return
	}
	c.Redirect(http.StatusFound, "/owner/payments?success="+url.QueryEscape("晚到成功及重复通知已发送"))
}

func (h *HTTPHandler) startAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	var input struct {
		BookingNo string `json:"booking_no"`
		RequestNo string `json:"request_no"`
		Scenario  string `json:"scenario"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	order, err := h.service.Start(c.Request.Context(), StartInput{OwnerID: identity.UserID, BookingNo: input.BookingNo, RequestNo: input.RequestNo, Scenario: input.Scenario})
	if err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, order)
}

func (h *HTTPHandler) listAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	views, err := h.service.ListOwner(c.Request.Context(), identity.UserID)
	if err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"payments": views})
}

func (h *HTTPHandler) notificationCallback(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 64*1024))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_NOTIFICATION"})
		return
	}
	notification, err := h.provider.ParsePaymentNotification(c.Request.Header, body)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "INVALID_SIGNATURE"})
		return
	}
	if err := h.service.HandleNotification(c.Request.Context(), notification, body); err != nil {
		writePaymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "accepted"})
}

func writePaymentError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "INTERNAL_ERROR"
	switch {
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrInvalidNotification):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	case errors.Is(err, ErrBookingNotFound), errors.Is(err, ErrPaymentNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, ErrRefundNotFound):
		status, code = http.StatusNotFound, "REFUND_NOT_FOUND"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, ErrInvalidBookingState), errors.Is(err, ErrPaymentExists):
		status, code = http.StatusConflict, "INVALID_STATE"
	case errors.Is(err, ErrRefundInProgress), errors.Is(err, ErrRefundNotAllowed):
		status, code = http.StatusConflict, "REFUND_INVALID_STATE"
	case errors.Is(err, ErrAmountMismatch):
		status, code = http.StatusConflict, "AMOUNT_MISMATCH"
	}
	c.JSON(status, gin.H{"error": code, "message": paymentErrorMessage(err)})
}

func paymentErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return "支付请求参数不正确"
	case errors.Is(err, ErrBookingNotFound):
		return "预约记录不存在"
	case errors.Is(err, ErrPaymentNotFound):
		return "支付记录不存在"
	case errors.Is(err, ErrForbidden):
		return "无权操作该支付记录"
	case errors.Is(err, ErrInvalidBookingState):
		return "当前预约状态不允许支付"
	case errors.Is(err, ErrPaymentExists):
		return "该预约已有一笔处理中支付，请勿重复发起"
	case errors.Is(err, ErrAmountMismatch):
		return "支付金额与预约金额不一致"
	case errors.Is(err, ErrInvalidNotification):
		return "支付通知内容无效"
	case errors.Is(err, ErrRefundNotFound):
		return "退款记录不存在"
	case errors.Is(err, ErrRefundInProgress):
		return "该退款正在处理中，请勿重复提交"
	case errors.Is(err, ErrRefundNotAllowed):
		return "当前退款状态不允许执行此操作"
	default:
		return "支付渠道暂时无法处理，请稍后重试"
	}
}

func refundStatusMessage(status string) string {
	labels := map[string]string{RefundPending: "待退款", RefundProcessing: "退款处理中", RefundUnknown: "退款待确认", RefundSucceeded: "退款成功", RefundFailed: "退款失败"}
	if label, ok := labels[status]; ok {
		return label
	}
	return status
}
