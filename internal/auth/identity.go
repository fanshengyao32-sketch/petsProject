package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type Identity struct {
	UserID      uint64
	DisplayName string
	Role        string
	StoreID     *uint64
}

var demoSessions = map[string]Identity{
	"owner-lin-session":   {UserID: 1, DisplayName: "林小满", Role: "owner"},
	"owner-zhao-session":  {UserID: 7, DisplayName: "赵晨", Role: "owner"},
	"owner-wang-session":  {UserID: 8, DisplayName: "王琪", Role: "owner"},
	"owner-chen-session":  {UserID: 9, DisplayName: "陈雨", Role: "owner"},
	"manager-xu-session":  {UserID: 2, DisplayName: "许岚", Role: "manager", StoreID: uint64Ptr(1)},
	"front-zhou-session":  {UserID: 3, DisplayName: "周宁", Role: "front_desk", StoreID: uint64Ptr(1)},
	"service-session":     {UserID: 4, DisplayName: "程安", Role: "service_staff", StoreID: uint64Ptr(1)},
	"support-gu-session":  {UserID: 5, DisplayName: "顾言", Role: "support"},
	"front-other-session": {UserID: 6, DisplayName: "外店前台", Role: "front_desk", StoreID: uint64Ptr(2)},
}

const identityKey = "trusted_identity"

const legacySessionCookie = "pet_session"

func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if identity, ok := identityFromCookie(c, legacySessionCookie); ok {
			c.Set(identityKey, identity)
		}
		c.Next()
	}
}

func DemoLogin(c *gin.Context) {
	token := c.Param("token")
	identity, ok := demoSessions[token]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown demo session"})
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     roleSessionCookie(identity.Role),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// Remove the old single-cookie session. Role-specific cookies can coexist,
	// so owner and manager pages work in separate tabs of the same browser.
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     legacySessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	redirect := "/"
	if identity.Role == "owner" {
		redirect = "/owner"
	} else if identity.Role == "manager" {
		redirect = "/manager"
	} else if identity.Role == "support" {
		redirect = "/support"
	} else if identity.Role == "front_desk" {
		redirect = "/front-desk"
	} else if identity.Role == "service_staff" {
		redirect = "/service-workbench"
	}
	c.Redirect(http.StatusFound, redirect)
}

func Current(c *gin.Context) (Identity, error) {
	value, exists := c.Get(identityKey)
	if !exists {
		return Identity{}, errors.New("authentication required")
	}
	identity, ok := value.(Identity)
	if !ok {
		return Identity{}, errors.New("invalid identity context")
	}
	return identity, nil
}

func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if identity, ok := identityFromCookie(c, roleSessionCookie(role)); ok && identity.Role == role {
			c.Set(identityKey, identity)
		}
		identity, err := Current(c)
		if err != nil {
			if !requestExpectsJSON(c) {
				c.Redirect(http.StatusFound, "/")
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "AUTH_REQUIRED", "message": "请先选择演示身份"})
			return
		}
		if identity.Role != role {
			if !requestExpectsJSON(c) {
				c.Redirect(http.StatusFound, "/")
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "ROLE_FORBIDDEN", "message": "当前角色无权执行此操作"})
			return
		}
		c.Next()
	}
}

func requestExpectsJSON(c *gin.Context) bool {
	return strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.Contains(c.GetHeader("Accept"), "application/json")
}

func identityFromCookie(c *gin.Context, cookieName string) (Identity, bool) {
	token, err := c.Cookie(cookieName)
	if err != nil {
		return Identity{}, false
	}
	identity, ok := demoSessions[token]
	return identity, ok
}

func roleSessionCookie(role string) string {
	return "pet_session_" + role
}

func uint64Ptr(value uint64) *uint64 { return &value }
