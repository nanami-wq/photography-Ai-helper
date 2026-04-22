package controller

import (
	"net/http"
	"strings"

	"go-service-starter/config"
	"go-service-starter/core/libx"
	"go-service-starter/core/store/mysql"
	"go-service-starter/repository"
	"go-service-starter/usecase"

	"github.com/gin-gonic/gin"
)

type AuthController struct {
	uc *usecase.AuthUsecase
}

func NewAuthController() *AuthController {
	db, err := mysql.InitMySQL()
	if err != nil {
		panic("mysql init failed: " + err.Error())
	}
	repo := repository.NewUserRepo(db)
	hours := config.GetConfig().JWT.AccessHours
	uc := usecase.NewAuthUsecase(repo, hours)
	return &AuthController{uc: uc}
}

func (a *AuthController) RegisterPublic(r *gin.RouterGroup) {
	r.POST("/auth/register", a.Register)
	r.POST("/auth/login", a.Login)
}

func (a *AuthController) RegisterProtected(r *gin.RouterGroup) {
	r.GET("/auth/me", a.Me)
}

type registerBody struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (a *AuthController) Register(c *gin.Context) {
	if a.uc == nil {
		libx.Err(c, http.StatusServiceUnavailable, "未启用 MySQL（config.mysql.skip=true 时无法使用认证）", nil)
		return
	}
	var body registerBody
	if err := c.ShouldBindJSON(&body); err != nil {
		libx.Err(c, http.StatusBadRequest, "参数无效", err)
		return
	}
	user, err := a.uc.Register(c.Request.Context(), usecase.RegisterInput{
		Username: body.Username,
		Email:    body.Email,
		Password: body.Password,
	})
	if err != nil {
		switch err {
		case usecase.ErrUserExists:
			libx.Err(c, http.StatusConflict, err.Error(), nil)
		case usecase.ErrPasswordTooShort:
			libx.Err(c, http.StatusBadRequest, err.Error(), nil)
		default:
			libx.Err(c, http.StatusInternalServerError, "注册失败", err)
		}
		return
	}
	libx.Ok(c, "注册成功", gin.H{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"role":     user.Role,
	})
}

type loginBody struct {
	Account  string `json:"account" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (a *AuthController) Login(c *gin.Context) {
	var body loginBody
	if err := c.ShouldBindJSON(&body); err != nil {
		libx.Err(c, http.StatusBadRequest, "参数无效", err)
		return
	}
	res, err := a.uc.Login(c.Request.Context(), usecase.LoginInput{
		Account:  strings.TrimSpace(body.Account),
		Password: body.Password,
	})
	if err != nil {
		if err == usecase.ErrInvalidAccount {
			libx.Err(c, http.StatusUnauthorized, err.Error(), nil)
			return
		}
		libx.Err(c, http.StatusInternalServerError, "登录失败", err)
		return
	}
	u := res.User
	libx.Ok(c, "登录成功", gin.H{
		"access_token": res.Token,
		"token_type":   "Bearer",
		"expires_in":   res.ExpiresIn,
		"user": gin.H{
			"id":       u.ID,
			"username": u.Username,
			"email":    u.Email,
			"role":     u.Role,
		},
	})
}

func (a *AuthController) Me(c *gin.Context) {
	uid := libx.Uid(c)
	user, err := a.uc.Me(c.Request.Context(), uid)
	if err != nil {
		libx.Err(c, http.StatusNotFound, "用户不存在", err)
		return
	}
	libx.Ok(c, "ok", gin.H{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"role":     user.Role,
	})
}
