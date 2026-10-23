package targetdb

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestDSNQuotesSpecialCharacters(t *testing.T) {
	params := ConnParams{
		Host:     "db.example.com",
		Port:     6543,
		DBName:   "my db",
		Username: "o'brien",
		Password: `p@ss word' host=evil.example.com \x`,
		SSLMode:  "disable",
	}

	cfg, err := pgconn.ParseConfig(params.DSN())
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Host != params.Host || cfg.Port != uint16(params.Port) {
		t.Errorf("host/port = %s:%d, want %s:%d", cfg.Host, cfg.Port, params.Host, params.Port)
	}
	if cfg.Database != params.DBName {
		t.Errorf("database = %q, want %q", cfg.Database, params.DBName)
	}
	if cfg.User != params.Username {
		t.Errorf("user = %q, want %q", cfg.User, params.Username)
	}
	if cfg.Password != params.Password {
		t.Errorf("password = %q, want %q", cfg.Password, params.Password)
	}
	if cfg.ConnectTimeout.Seconds() != ConnectTimeoutSeconds {
		t.Errorf("connect timeout = %v, want %ds", cfg.ConnectTimeout, ConnectTimeoutSeconds)
	}
}
