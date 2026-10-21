package userhandler

import (
	"web_backend/internal/models"
)

func CreateUser(name, email, password string) *models.User {
	return &models.User{
		Name:     name,
		Email:    email,
		Password: password,
	}
}
