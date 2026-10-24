package models

import "time"

type ErrorResponse struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

type HealthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

type EchoTestResponse struct {
	Status   string         `json:"status"`
	Message  string         `json:"message"`
	Database string         `json:"database"`
	Echo     map[string]any `json:"echo,omitempty"`
}

type LoginResponse struct {
	Message string `json:"message"`
	User    User   `json:"user"`
}

type UserListResponse struct {
	Users []User `json:"users"`
}

type ConnectionListResponse struct {
	Connections []DBConnection `json:"connections"`
}

type ConnectionTestResponse struct {
	Status        string `json:"status"`
	ServerVersion string `json:"server_version"`
}

type GroupListResponse struct {
	Groups []Group `json:"groups"`
}

type GroupMemberView struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type GroupMemberListResponse struct {
	Members []GroupMemberView `json:"members"`
}

type GroupAccessView struct {
	GroupID        string      `json:"group_id"`
	ConnectionID   string      `json:"connection_id"`
	ConnectionName string      `json:"connection_name"`
	Level          AccessLevel `json:"level"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type GroupAccessListResponse struct {
	Access []GroupAccessView `json:"access"`
}
