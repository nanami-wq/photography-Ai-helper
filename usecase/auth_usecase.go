package usecase

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"go-service-starter/api/middleware"
	"go-service-starter/domain"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrUserExists       = errors.New("用户名或邮箱已被占用")
	ErrInvalidAccount   = errors.New("账号或密码错误")
	ErrPasswordTooShort = errors.New("密码长度至少 8 位")
)

type AuthUsecase struct {
	users  domain.UserRepository
	jwtTTL time.Duration
}

func NewAuthUsecase(users domain.UserRepository, accessHours int) *AuthUsecase {
	if accessHours <= 0 {
		accessHours = 24
	}
	return &AuthUsecase{
		users:  users,
		jwtTTL: time.Duration(accessHours) * time.Hour,
	}
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

func (u *AuthUsecase) Register(ctx context.Context, in RegisterInput) (*domain.User, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	in.Username = strings.TrimSpace(in.Username)
	if utf8.RuneCountInString(in.Password) < 8 {
		return nil, ErrPasswordTooShort
	}
	if in.Email == "" || in.Username == "" {
		return nil, errors.New("用户名与邮箱不能为空")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	user := &domain.User{
		Username:     in.Username,
		Email:        in.Email,
		PasswordHash: string(hash),
		Role:         "user",
	}
	if err := u.users.Create(ctx, user); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	return user, nil
}

type LoginInput struct {
	Account  string
	Password string
}

type LoginResult struct {
	Token     string
	ExpiresIn int64
	User      *domain.User
}

func (u *AuthUsecase) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	in.Account = strings.TrimSpace(in.Account)
	if in.Account == "" || in.Password == "" {
		return nil, ErrInvalidAccount
	}
	var user *domain.User
	var err error
	if strings.Contains(in.Account, "@") {
		email := strings.ToLower(in.Account)
		user, err = u.users.GetByEmail(ctx, email)
	} else {
		user, err = u.users.GetByUsername(ctx, in.Account)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidAccount
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(in.Password)); err != nil {
		return nil, ErrInvalidAccount
	}
	token, err := middleware.SignToken(user.ID, user.Username, user.Role, u.jwtTTL)
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		Token:     token,
		ExpiresIn: int64(u.jwtTTL.Seconds()),
		User:      user,
	}, nil
}

func (u *AuthUsecase) Me(ctx context.Context, id uint) (*domain.User, error) {
	return u.users.GetByID(ctx, id)
}
