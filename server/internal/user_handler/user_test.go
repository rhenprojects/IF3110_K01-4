package userhandler

import (
	"errors"
	"strings"
	"testing"
	"web_backend/internal/models"
)

func TestValidateCreateUser(t *testing.T) {
	valid := models.CreateUserRequest{Username: "alice", Password: "password123"}

	req := valid
	if err := ValidateCreateUser(&req); err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if req.Name != "alice" {
		t.Errorf("name defaults to username: got %q", req.Name)
	}

	cases := []struct {
		field  string
		mutate func(*models.CreateUserRequest)
	}{
		{"username", func(r *models.CreateUserRequest) { r.Username = "ab" }},
		{"username", func(r *models.CreateUserRequest) { r.Username = "has space" }},
		{"username", func(r *models.CreateUserRequest) { r.Username = "x' OR '1'='1" }},
		{"name", func(r *models.CreateUserRequest) { r.Name = strings.Repeat("n", 101) }},
		{"password", func(r *models.CreateUserRequest) { r.Password = "short" }},
		{"password", func(r *models.CreateUserRequest) { r.Password = strings.Repeat("p", 73) }},
	}
	for _, tc := range cases {
		req := valid
		tc.mutate(&req)
		err := ValidateCreateUser(&req)
		var verr *ValidationError
		if !errors.As(err, &verr) || verr.Field != tc.field {
			t.Errorf("%+v: err = %v, want ValidationError on %q", req, err, tc.field)
		}
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct-horse" || strings.Contains(hash, "correct-horse") {
		t.Fatal("hash contains plaintext password")
	}
	if !CheckPassword(hash, "correct-horse") {
		t.Error("correct password rejected")
	}
	if CheckPassword(hash, "wrong-horse") {
		t.Error("wrong password accepted")
	}
	if CheckPassword("", "anything") {
		t.Error("empty hash (unknown user) accepted")
	}
}
