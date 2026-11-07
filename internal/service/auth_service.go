package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"homework/internal/auth"
	"homework/internal/model"
	"homework/internal/repository"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

var dummyPasswordHash = func() string {
	hash, err := bcrypt.GenerateFromPassword([]byte("dummy-authentication-password"), bcrypt.DefaultCost)
	if err != nil {
		panic("initialize dummy password hash")
	}
	return string(hash)
}()

type TokenManager interface {
	Issue(user model.User) (string, error)
	TTL() time.Duration
}

type AuthService struct {
	users  repository.UserRepository
	tokens TokenManager
}

func NewAuthService(users repository.UserRepository, tokens TokenManager) *AuthService {
	return &AuthService{users: users, tokens: tokens}
}

func (service *AuthService) Register(ctx context.Context, request model.RegisterRequest) (model.UserResponse, error) {
	email, err := validateRegistration(request)
	if err != nil {
		return model.UserResponse{}, err
	}
	passwordHash, err := auth.HashPassword(request.Password)
	if err != nil {
		var passwordError auth.PasswordValidationError
		if errors.As(err, &passwordError) {
			return model.UserResponse{}, ValidationError{Message: passwordError.Error()}
		}
		return model.UserResponse{}, fmt.Errorf("hash password: %w", err)
	}
	user := model.User{
		Email:        email,
		Name:         strings.TrimSpace(request.Name),
		PasswordHash: passwordHash,
		Role:         model.RoleViewer,
	}
	if err := service.users.Create(ctx, &user); err != nil {
		if errors.Is(err, repository.ErrEmailTaken) {
			return model.UserResponse{}, ConflictError{Message: "email already registered"}
		}
		return model.UserResponse{}, fmt.Errorf("create user: %w", err)
	}
	return user.ToResponse(), nil
}

func (service *AuthService) EnsureAdmin(ctx context.Context, email, password string) (bool, error) {
	normalized := normalizeAuthEmail(email)
	_, err := service.users.FindByEmail(ctx, normalized)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return false, fmt.Errorf("find admin by email: %w", err)
	}
	created, err := service.Register(ctx, model.RegisterRequest{
		Email:    email,
		Password: password,
		Name:     "Admin",
	})
	if err != nil {
		var conflictError ConflictError
		if errors.As(err, &conflictError) {
			return false, nil
		}
		return false, err
	}
	if _, err := service.users.UpdateRole(ctx, created.ID, model.RoleAdmin); err != nil {
		return false, fmt.Errorf("promote seeded admin: %w", err)
	}
	return true, nil
}

func (service *AuthService) Login(ctx context.Context, request model.LoginRequest) (model.TokenResponse, error) {
	email := normalizeAuthEmail(request.Email)
	user, err := service.users.FindByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return model.TokenResponse{}, fmt.Errorf("find user for login: %w", err)
		}
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(request.Password))
		return model.TokenResponse{}, ErrUnauthorized
	}
	if auth.CheckPassword(user.PasswordHash, request.Password) != nil {
		return model.TokenResponse{}, ErrUnauthorized
	}
	accessToken, err := service.tokens.Issue(user)
	if err != nil {
		return model.TokenResponse{}, fmt.Errorf("issue access token: %w", err)
	}
	return model.TokenResponse{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(service.tokens.TTL().Seconds()),
	}, nil
}

func validateRegistration(request model.RegisterRequest) (string, error) {
	email := strings.TrimSpace(request.Email)
	if len(email) == 0 || len(email) > 254 {
		return "", ValidationError{Message: "email must be a valid address"}
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", ValidationError{Message: "email must be a valid address"}
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len(name) > 100 {
		return "", ValidationError{Message: "name must be between 1 and 100 bytes"}
	}
	if len(request.Password) < 8 || len(request.Password) > 72 {
		return "", ValidationError{Message: "password must be between 8 and 72 bytes"}
	}
	return strings.ToLower(email), nil
}

func normalizeAuthEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
