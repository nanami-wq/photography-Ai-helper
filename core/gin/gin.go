package gin

import (
	"go-service-starter/api/middleware"
	"go-service-starter/api/router"
	"go-service-starter/config"

	"github.com/gin-gonic/gin"
)

func GinInit() *gin.Engine {
	r := gin.Default()
	config.MustLoad()
	router.GenerateRouter(r)
	middleware.InitSecret(config.GetConfig().JWT.Secret)
	return r
}
