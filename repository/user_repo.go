package repository

import (
	"context"

	"go-service-starter/domain"

	"gorm.io/gorm"
)

type UserRepo struct {
	db *gorm.DB
}

func NewUserRepo(db *gorm.DB) domain.UserRepository {
	return &UserRepo{db: db}
}

func (r *UserRepo) Create(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).Where("username = ?", username).First(&u).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uint) (*domain.User, error) {
	var u domain.User
	if err := r.db.WithContext(ctx).First(&u, id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}
