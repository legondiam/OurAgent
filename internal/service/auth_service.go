package service

import (
	"strconv"
	"time"

	"OurAgent/internal/model"
	"OurAgent/internal/repository"

	"github.com/golang-jwt/jwt/v5"
	pkgerrors "github.com/pkg/errors"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	users        *repository.UserRepository
	jwtSecret    string
	expiresHours int
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type LoginInput struct {
	Username string
	Password string
}

type LoginResult struct {
	Token string    `json:"token"`
	User  LoginUser `json:"user"`
}

type LoginUser struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

func NewAuthService(users *repository.UserRepository, jwtSecret string, expiresHours int) *AuthService {
	return &AuthService{users: users, jwtSecret: jwtSecret, expiresHours: expiresHours}
}

// Register 注册用户
func (s *AuthService) Register(input RegisterInput) (*model.User, error) {
	count, err := s.users.CountByUsername(input.Username)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "查询用户名失败")
	}
	if count > 0 {
		return nil, pkgerrors.WithStack(ErrUserExisted)
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "密码加密失败")
	}

	user := &model.User{
		Username:     input.Username,
		Email:        input.Email,
		PasswordHash: string(passwordHash),
	}
	if err := s.users.Create(user); err != nil {
		return nil, pkgerrors.WithMessage(err, "创建用户失败")
	}
	return user, nil
}

// Login 校验用户密码并生成 token
func (s *AuthService) Login(input LoginInput) (*LoginResult, error) {
	user, err := s.users.FindByUsername(input.Username)
	if err != nil {
		return nil, pkgerrors.WithStack(ErrAccountOrPassword)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)); err != nil {
		return nil, pkgerrors.WithStack(ErrAccountOrPassword)
	}

	expiresAt := time.Now().Add(time.Duration(s.expiresHours) * time.Hour)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatUint(user.ID, 10),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	})
	tokenString, err := token.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return nil, pkgerrors.WithMessage(err, "生成 token 失败")
	}

	return &LoginResult{
		Token: tokenString,
		User:  LoginUser{ID: user.ID, Username: user.Username},
	}, nil
}
