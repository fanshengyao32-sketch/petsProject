package booking

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
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
	router.GET("/owner", auth.RequireRole("owner"), h.ownerPage)
	router.GET("/manager", auth.RequireRole("manager"), h.managerPage)
	router.POST("/owner/bookings", auth.RequireRole("owner"), h.createBookingForm)
	router.POST("/owner/bookings/:bookingNo/cancel", auth.RequireRole("owner"), h.cancelUnpaidForm)
	router.POST("/manager/slots", auth.RequireRole("manager"), h.createSlotForm)

	api := router.Group("/api")
	api.GET("/slots", h.listSlotsAPI)
	api.POST("/bookings", auth.RequireRole("owner"), h.createBookingAPI)
	api.POST("/bookings/:bookingNo/cancel-unpaid", auth.RequireRole("owner"), h.cancelUnpaidAPI)
	api.POST("/slots", auth.RequireRole("manager"), h.createSlotAPI)
}

func (h *HTTPHandler) ownerPage(c *gin.Context) {
	identity, _ := auth.Current(c)
	slots, err := h.service.ListSlots(c.Request.Context(), 1)
	if err != nil {
		h.renderError(c, err)
		return
	}
	bookings, err := h.service.ListOwnerBookings(c.Request.Context(), identity.UserID)
	if err != nil {
		h.renderError(c, err)
		return
	}
	pets, err := h.service.ListOwnerPets(c.Request.Context(), identity.UserID)
	if err != nil {
		h.renderError(c, err)
		return
	}
	c.HTML(http.StatusOK, "owner.html", gin.H{"Identity": identity, "Slots": slots, "Bookings": bookings, "Pets": pets, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) managerPage(c *gin.Context) {
	identity, _ := auth.Current(c)
	if identity.StoreID == nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	slots, err := h.service.ListSlots(c.Request.Context(), *identity.StoreID)
	if err != nil {
		h.renderError(c, err)
		return
	}
	c.HTML(http.StatusOK, "manager.html", gin.H{"Identity": identity, "Slots": slots, "Error": c.Query("error"), "Success": c.Query("success")})
}

func (h *HTTPHandler) createBookingForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	petID, _ := strconv.ParseUint(c.PostForm("pet_id"), 10, 64)
	slotID, _ := strconv.ParseUint(c.PostForm("slot_id"), 10, 64)
	requestKey := c.PostForm("request_key")
	_, err := h.service.CreateBooking(c.Request.Context(), CreateBookingInput{OwnerID: identity.UserID, PetID: petID, SlotID: slotID, RequestKey: requestKey})
	if err != nil {
		c.Redirect(http.StatusFound, "/owner?error="+urlMessage(err))
		return
	}
	c.Redirect(http.StatusFound, "/owner/payments?success="+url.QueryEscape("预约已创建，名额保留5分钟，请立即选择支付结果"))
}

func (h *HTTPHandler) cancelUnpaidForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	_, err := h.service.CancelUnpaid(c.Request.Context(), identity.UserID, c.Param("bookingNo"))
	if err != nil {
		c.Redirect(http.StatusFound, "/owner?error="+urlMessage(err))
		return
	}
	c.Redirect(http.StatusFound, "/owner?success=待支付预约已取消，名额已释放")
}

func (h *HTTPHandler) createSlotForm(c *gin.Context) {
	identity, _ := auth.Current(c)
	startsAt, err1 := time.ParseInLocation("2006-01-02T15:04", c.PostForm("starts_at"), h.storeLocation)
	endsAt, err2 := time.ParseInLocation("2006-01-02T15:04", c.PostForm("ends_at"), h.storeLocation)
	if err1 != nil || err2 != nil || identity.StoreID == nil {
		c.Redirect(http.StatusFound, "/manager?error=时间格式错误")
		return
	}
	_, err := h.service.CreateSlot(c.Request.Context(), CreateSlotInput{ManagerID: identity.UserID, StoreID: *identity.StoreID, ServiceItemID: 1, StartsAt: startsAt, EndsAt: endsAt})
	if err != nil {
		c.Redirect(http.StatusFound, "/manager?error="+urlMessage(err))
		return
	}
	c.Redirect(http.StatusFound, "/manager?success=时段发布成功")
}

func (h *HTTPHandler) listSlotsAPI(c *gin.Context) {
	slots, err := h.service.ListSlots(c.Request.Context(), 1)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"slots": slots})
}

func (h *HTTPHandler) createBookingAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	var input struct {
		PetID      uint64 `json:"pet_id"`
		SlotID     uint64 `json:"slot_id"`
		RequestKey string `json:"request_key"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	result, err := h.service.CreateBooking(c.Request.Context(), CreateBookingInput{OwnerID: identity.UserID, PetID: input.PetID, SlotID: input.SlotID, RequestKey: input.RequestKey})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *HTTPHandler) cancelUnpaidAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	result, err := h.service.CancelUnpaid(c.Request.Context(), identity.UserID, c.Param("bookingNo"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *HTTPHandler) createSlotAPI(c *gin.Context) {
	identity, _ := auth.Current(c)
	if identity.StoreID == nil {
		writeError(c, ErrForbidden)
		return
	}
	var input struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "INVALID_REQUEST"})
		return
	}
	result, err := h.service.CreateSlot(c.Request.Context(), CreateSlotInput{ManagerID: identity.UserID, StoreID: *identity.StoreID, ServiceItemID: 1, StartsAt: input.StartsAt, EndsAt: input.EndsAt})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

func (h *HTTPHandler) renderError(c *gin.Context, err error) {
	c.HTML(http.StatusInternalServerError, "error.html", gin.H{"Message": businessErrorMessage(err)})
}

func writeError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "INTERNAL_ERROR"
	switch {
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrInvalidSlotTime):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	case errors.Is(err, ErrSlotNotFound), errors.Is(err, ErrBookingNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, ErrCapacityFull):
		status, code = http.StatusConflict, "CAPACITY_FULL"
	case errors.Is(err, ErrDuplicateActive):
		status, code = http.StatusConflict, "DUPLICATE_ACTIVE_BOOKING"
	case errors.Is(err, ErrSlotStarted), errors.Is(err, ErrInvalidBookingState), errors.Is(err, ErrSlotOverlap):
		status, code = http.StatusConflict, "INVALID_STATE"
	case errors.Is(err, ErrPetNotOwned), errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	}
	c.JSON(status, gin.H{"error": code, "message": businessErrorMessage(err)})
}

func urlMessage(err error) string { return url.QueryEscape(businessErrorMessage(err)) }

func businessErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrSlotNotFound):
		return "预约时段不存在"
	case errors.Is(err, ErrSlotStarted):
		return "预约时段已经开始，无法预约"
	case errors.Is(err, ErrCapacityFull):
		return "该时段名额已满"
	case errors.Is(err, ErrPetNotOwned):
		return "只能为本人名下的宠物预约"
	case errors.Is(err, ErrDuplicateActive):
		return "该宠物在这个时段已有一笔有效预约，请勿重复预约"
	case errors.Is(err, ErrBookingNotFound):
		return "预约记录不存在"
	case errors.Is(err, ErrInvalidBookingState):
		return "当前预约状态不允许执行此操作"
	case errors.Is(err, ErrForbidden):
		return "无权执行此操作"
	case errors.Is(err, ErrSlotOverlap):
		return "该服务时段与已有时段重叠"
	case errors.Is(err, ErrInvalidRequest):
		return "请求参数不正确"
	case errors.Is(err, ErrInvalidSlotTime):
		return "结束时间必须晚于开始时间，且开始时间必须在未来"
	default:
		return "系统暂时无法处理该操作，请稍后重试"
	}
}
