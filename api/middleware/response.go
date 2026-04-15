package middleware

import (
	"fmt"

	"go-service-starter/core/libx"

	"github.com/gin-gonic/gin"
)

type Response struct {
	Code    any         `json:"code"`
	ErrCode any         `json:"err_code"`
	Data    interface{} `json:"data,omitempty"`
	Msg     string      `json:"message,omitempty"`
}

func ResponseMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if c.Writer.Written() {
			return
		}
		status := c.Writer.Status()
		if v, exists := c.Get(libx.HTTPStatusKey); exists {
			if s, ok := v.(int); ok {
				status = s
			}
		}
		var data interface{}
		if c.Keys != nil {
			data = c.Keys["data"]
		}
		msg := c.Keys["message"]
		code := c.Keys["code"]
		if code == nil {
			code = status
		}
		if status == 404 && msg == nil {
			msg = "Not Found"
		}
		errCode := code
		if codeInt, ok := code.(int); ok && codeInt == 200 {
			errCode = 200
		}
		c.JSON(status, Response{
			Code:    code,
			ErrCode: errCode,
			Data:    data,
			Msg:     fmt.Sprintf("%v", msg),
		})
	}
}
