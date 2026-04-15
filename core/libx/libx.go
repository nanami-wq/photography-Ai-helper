package libx

import (
	"log"

	"github.com/gin-gonic/gin"
)

func Uid(c *gin.Context) uint {
	return c.MustGet("uid").(uint)
}

func GetUsername(c *gin.Context) string {
	return c.MustGet("username").(string)
}

const HTTPStatusKey = "http_status"

func Code(c *gin.Context, code int) {
	c.Set(HTTPStatusKey, code)
}

func Msg(c *gin.Context, msg string) {
	c.Set("message", msg)
}

func Data(c *gin.Context, data interface{}) {
	c.Set("data", data)
}

func Err(c *gin.Context, code any, msg string, err error) {
	codeInt, ok := code.(int)
	if ok {
		Code(c, codeInt)
	} else {
		Code(c, 500)
	}
	var errorMsg string
	if err != nil {
		errorMsg = err.Error()
	}
	c.Set("code", code)
	c.Set("message", msg+" "+errorMsg)
	log.Println(msg + " " + errorMsg)
}

func Ok(c *gin.Context, input ...interface{}) {
	if len(input) >= 3 {
		log.Println("too many parameters")
		Err(c, 500, "参数过多，请后端开发人员排查", nil)
	}
	Code(c, 200)
	if len(input) == 2 {
		Msg(c, input[0].(string))
		Data(c, input[1])
	} else {
		Msg(c, input[0].(string))
		Data(c, nil)
	}
}
