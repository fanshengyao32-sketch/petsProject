package fulfillment

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/fanshengyao32-sketch/petsProject/internal/auth"
	"github.com/gin-gonic/gin"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) *HTTPHandler { return &HTTPHandler{service: service} }

func (h *HTTPHandler) Register(router *gin.Engine) {
	router.GET("/front-desk", auth.RequireRole("front_desk"), h.frontDeskPage)
	router.POST("/front-desk/bookings/:bookingNo/check-in", auth.RequireRole("front_desk"), h.checkInForm)
	router.GET("/service-workbench", auth.RequireRole("service_staff"), h.servicePage)
	router.POST("/service-workbench/bookings/:bookingNo/start", auth.RequireRole("service_staff"), h.startForm)
	router.POST("/service-workbench/bookings/:bookingNo/complete", auth.RequireRole("service_staff"), h.completeForm)
	router.POST("/api/fulfillment/:bookingNo/check-in", auth.RequireRole("front_desk"), h.checkInAPI)
	router.POST("/api/fulfillment/:bookingNo/start", auth.RequireRole("service_staff"), h.startAPI)
	router.POST("/api/fulfillment/:bookingNo/complete", auth.RequireRole("service_staff"), h.completeAPI)
}

func (h *HTTPHandler) frontDeskPage(c *gin.Context) { h.renderWorkPage(c, "front-desk.html") }
func (h *HTTPHandler) servicePage(c *gin.Context)   { h.renderWorkPage(c, "service-workbench.html") }

func (h *HTTPHandler) renderWorkPage(c *gin.Context, templateName string) {
	identity, _ := auth.Current(c)
	if identity.StoreID == nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	views, err := h.service.ListStore(c.Request.Context(), *identity.StoreID)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": fulfillmentErrorMessage(err)})
		return
	}
	c.HTML(http.StatusOK, templateName, gin.H{"Identity": identity, "Bookings": views, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) checkInForm(c *gin.Context) {
	h.operateForm(c, "/front-desk", h.service.CheckIn, "核销成功")
}
func (h *HTTPHandler) startForm(c *gin.Context) {
	h.operateForm(c, "/service-workbench", h.service.StartService, "服务开始已登记")
}
func (h *HTTPHandler) completeForm(c *gin.Context) {
	h.operateForm(c, "/service-workbench", h.service.CompleteService, "服务完成已登记")
}

type operation func(context.Context, OperateInput) (Event, error)

func (h *HTTPHandler) operateForm(c *gin.Context, target string, operation operation, success string) {
	identity, _ := auth.Current(c)
	_, err := operation(c.Request.Context(), OperateInput{OperatorID: identity.UserID, BookingNo: c.Param("bookingNo"), IdempotencyKey: c.PostForm("idempotency_key")})
	if err != nil {
		c.Redirect(http.StatusFound, target+"?error="+url.QueryEscape(fulfillmentErrorMessage(err)))
		return
	}
	c.Redirect(http.StatusFound, target+"?success="+url.QueryEscape(success))
}

func (h *HTTPHandler) checkInAPI(c *gin.Context)  { h.operateAPI(c, h.service.CheckIn) }
func (h *HTTPHandler) startAPI(c *gin.Context)    { h.operateAPI(c, h.service.StartService) }
func (h *HTTPHandler) completeAPI(c *gin.Context) { h.operateAPI(c, h.service.CompleteService) }

func (h *HTTPHandler) operateAPI(c *gin.Context, operation operation) {
	identity, _ := auth.Current(c)
	var input struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST", "message": "履约请求参数不正确"})
		return
	}
	result, err := operation(c.Request.Context(), OperateInput{OperatorID: identity.UserID, BookingNo: c.Param("bookingNo"), IdempotencyKey: input.IdempotencyKey})
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
	case errors.Is(err, ErrInvalidBookingState), errors.Is(err, ErrCancellationPending):
		status, code = http.StatusConflict, "INVALID_STATE"
	}
	c.JSON(status, gin.H{"error": code, "message": fulfillmentErrorMessage(err)})
}

func fulfillmentErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return "履约请求参数不正确"
	case errors.Is(err, ErrBookingNotFound):
		return "预约记录不存在"
	case errors.Is(err, ErrForbidden):
		return "只能由本店对应岗位员工操作"
	case errors.Is(err, ErrCancellationPending):
		return "该预约正在等待取消审核，暂时不能核销"
	case errors.Is(err, ErrInvalidBookingState):
		return "当前预约状态不允许执行此履约操作"
	default:
		return "履约操作暂时无法处理，请稍后重试"
	}
}
