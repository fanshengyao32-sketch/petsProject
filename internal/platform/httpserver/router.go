package httpserver

import (
	"database/sql"
	"html/template"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type Dependencies struct {
	DB            *sql.DB
	StoreTimezone string
	Template      *template.Template
}

func NewRouter(deps Dependencies) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	if deps.Template != nil {
		router.SetHTMLTemplate(deps.Template)
	}

	router.GET("/health/live", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		if deps.DB == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "database unavailable"})
			return
		}
		ctx, cancel := timeoutContext(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := deps.DB.PingContext(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "reason": "database unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	router.GET("/", func(c *gin.Context) {
		if deps.Template == nil {
			c.JSON(http.StatusOK, gin.H{"service": "pet-booking", "timezone": deps.StoreTimezone})
			return
		}
		c.HTML(http.StatusOK, "home.html", gin.H{"StoreTimezone": deps.StoreTimezone})
	})
	return router
}
