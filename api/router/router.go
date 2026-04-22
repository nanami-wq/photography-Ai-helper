package router

import (
	"go-service-starter/api/controller"
	"go-service-starter/api/middleware"

	"github.com/gin-gonic/gin"
)

func GenerateRouter(r *gin.Engine) {
	r.Use(gin.Recovery(), middleware.CorsWare(), middleware.ResponseMiddleware())
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	auth := controller.NewAuthController()
	auth.RegisterPublic(r.Group("/api"))

	gemini := controller.NewGeminiController()
	gemini.RegisterPublic(r.Group("/api"))

	protected := r.Group("/api")
	protected.Use(middleware.JWTAuthMiddleware())
	auth.RegisterProtected(protected)
}
