package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequireRoleUsesTrustedSessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/owner-only", RequireRole("owner"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := httptest.NewRequest(http.MethodGet, "/owner-only", nil)
	request.AddCookie(&http.Cookie{Name: "pet_session", Value: "owner-lin-session"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)

	request = httptest.NewRequest(http.MethodGet, "/owner-only", nil)
	request.AddCookie(&http.Cookie{Name: "pet_session", Value: "manager-xu-session"})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusFound, response.Code)
	require.Equal(t, "/", response.Header().Get("Location"))
}

func TestUnknownSessionCannotAuthenticate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/owner-only", RequireRole("owner"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/owner-only", nil)
	request.AddCookie(&http.Cookie{Name: "pet_session", Value: "forged-session"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusFound, response.Code)
	require.Equal(t, "/", response.Header().Get("Location"))
}

func TestRequireRoleKeepsJSONErrorsForAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/api/owner-only", RequireRole("owner"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/owner-only", nil))
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Contains(t, response.Body.String(), "请先选择演示身份")

	request := httptest.NewRequest(http.MethodGet, "/api/owner-only", nil)
	request.AddCookie(&http.Cookie{Name: "pet_session_manager", Value: "manager-xu-session"})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestDemoLoginAndIdentityErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/login/:token", DemoLogin)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login/unknown", nil))
	require.Equal(t, http.StatusNotFound, response.Code)

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login/owner-lin-session", nil))
	require.Equal(t, http.StatusFound, response.Code)
	require.Contains(t, response.Header().Get("Set-Cookie"), "HttpOnly")
	require.Contains(t, strings.Join(response.Header().Values("Set-Cookie"), ";"), "pet_session_owner=owner-lin-session")

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	_, err := Current(ctx)
	require.Error(t, err)
	ctx.Set(identityKey, "wrong type")
	_, err = Current(ctx)
	require.Error(t, err)
}

func TestAllPresetOwnersCanUseDemoSessions(t *testing.T) {
	for _, token := range []string{"owner-lin-session", "owner-zhao-session", "owner-wang-session", "owner-chen-session"} {
		identity, ok := demoSessions[token]
		require.True(t, ok, token)
		require.Equal(t, "owner", identity.Role)
		require.NotZero(t, identity.UserID)
	}
}

func TestRoleSessionsCanCoexistAcrossBrowserTabs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Middleware())
	router.GET("/owner", RequireRole("owner"), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/manager", RequireRole("manager"), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, target := range []string{"/owner", "/manager"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.AddCookie(&http.Cookie{Name: "pet_session_owner", Value: "owner-lin-session"})
		request.AddCookie(&http.Cookie{Name: "pet_session_manager", Value: "manager-xu-session"})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusNoContent, response.Code, target)
	}
}
