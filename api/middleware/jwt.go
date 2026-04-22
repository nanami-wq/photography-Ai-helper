package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
)

var Secret []byte

func InitSecret(secret string) {
	Secret = []byte(secret)
}

func SignToken(userID uint, username, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(Secret)
}

type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		switch {
		case auth == "":
			token = strings.TrimSpace(c.Query("token"))
			if token == "" {
				c.JSON(http.StatusUnauthorized, gin.H{
					"code": http.StatusUnauthorized,
					"msg":  "请求未携带token，无权限访问",
				})
				c.Abort()
				return
			}
		case len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer "):
			token = strings.TrimSpace(auth[7:])
			if token == "" {
				c.JSON(http.StatusUnauthorized, gin.H{
					"code": http.StatusUnauthorized,
					"msg":  "Authorization: Bearer 后未提供有效 token",
				})
				c.Abort()
				return
			}
		default:
			token = auth
		}

		claims, err := ParseToken(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code": http.StatusUnauthorized,
				"msg":  err.Error(),
			})
			c.Abort()
			return
		}

		c.Set("uid", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

func ParseToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return Secret, nil
	})
	if err != nil {
		if ve, ok := err.(*jwt.ValidationError); ok {
			if ve.Errors&jwt.ValidationErrorExpired != 0 {
				return nil, errors.New("token已过期")
			}
			if ve.Errors&jwt.ValidationErrorSignatureInvalid != 0 {
				return nil, errors.New("token签名无效")
			}
			return nil, errors.New("token解析错误: " + err.Error())
		}
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}
