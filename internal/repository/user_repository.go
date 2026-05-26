package repository

import (
	"OurAgent/internal/model"

	pkgerrors "github.com/pkg/errors"
	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// CountByUsername 统计指定用户名数量
func (r *UserRepository) CountByUsername(username string) (int64, error) {
	var count int64
	if err := r.db.Model(&model.User{}).Where("username = ?", username).Count(&count).Error; err != nil {
		return 0, pkgerrors.WithMessage(err, "统计用户名失败")
	}
	return count, nil
}

// Create 创建用户记录
func (r *UserRepository) Create(user *model.User) error {
	if err := r.db.Create(user).Error; err != nil {
		return pkgerrors.WithMessage(err, "创建用户失败")
	}
	return nil
}

// FindByUsername 按用户名查询用户
func (r *UserRepository) FindByUsername(username string) (*model.User, error) {
	var user model.User
	if err := r.db.Where("username = ?", username).First(&user).Error; err != nil {
		return nil, pkgerrors.WithMessage(err, "查询用户失败")
	}
	return &user, nil
}
