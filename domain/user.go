package domain

import (
	"context"
	"time"
)

type User struct {
	ID           uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Email        string    `gorm:"size:255;uniqueIndex;not null" json:"email"`
	PasswordHash string    `gorm:"size:255;not null" json:"-"`
	Role         string    `gorm:"size:32;default:user;not null" json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id uint) (*User, error)
}
