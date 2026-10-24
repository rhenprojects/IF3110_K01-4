package targetdb

import (
	"context"
	"errors"
	"strings"
	"web_backend/internal/authz"
	"web_backend/internal/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

var validSSLModes = map[string]bool{
	"disable": true, "allow": true, "prefer": true,
	"require": true, "verify-ca": true, "verify-full": true,
}

type ConnectionInput struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	DBName   string `json:"db_name"`
	Username string `json:"username"`
	Password string `json:"password"`
	SSLMode  string `json:"ssl_mode"`
}

func (in *ConnectionInput) normalize(requirePassword bool) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.DBName = strings.TrimSpace(in.DBName)
	in.Username = strings.TrimSpace(in.Username)
	in.SSLMode = strings.TrimSpace(in.SSLMode)
	if in.Port == 0 {
		in.Port = 5432
	}
	if in.SSLMode == "" {
		in.SSLMode = "prefer"
	}

	switch {
	case in.Name == "" || len(in.Name) > 100:
		return &ValidationError{"name", "required, max 100 characters"}
	case in.Host == "" || len(in.Host) > 255:
		return &ValidationError{"host", "required, max 255 characters"}
	case strings.ContainsAny(in.Host, ",/ \t\r\n"):
		return &ValidationError{"host", "must be a single hostname or IP address"}
	case in.Port < 1 || in.Port > 65535:
		return &ValidationError{"port", "must be between 1 and 65535"}
	case in.DBName == "" || len(in.DBName) > 100:
		return &ValidationError{"db_name", "required, max 100 characters"}
	case in.Username == "" || len(in.Username) > 100:
		return &ValidationError{"username", "required, max 100 characters"}
	case requirePassword && in.Password == "":
		return &ValidationError{"password", "required"}
	case !validSSLModes[in.SSLMode]:
		return &ValidationError{"ssl_mode", "must be one of disable, allow, prefer, require, verify-ca, verify-full"}
	}
	return nil
}

func (in *ConnectionInput) params() ConnParams {
	return ConnParams{
		Host: in.Host, Port: in.Port, DBName: in.DBName,
		Username: in.Username, Password: in.Password, SSLMode: in.SSLMode,
	}
}

type Service struct {
	db     *gorm.DB
	cipher *Cipher
	pools  *PoolManager
}

func NewService(db *gorm.DB, cipher *Cipher, pools *PoolManager) *Service {
	return &Service{db: db, cipher: cipher, pools: pools}
}

func (s *Service) List(ctx context.Context) ([]models.DBConnection, error) {
	var conns []models.DBConnection
	err := s.db.WithContext(ctx).Order("name").Find(&conns).Error
	return conns, err
}

func (s *Service) Get(ctx context.Context, id string) (*models.DBConnection, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	var conn models.DBConnection
	if err := s.db.WithContext(ctx).First(&conn, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &conn, nil
}

func (s *Service) Create(ctx context.Context, in ConnectionInput, createdBy string) (*models.DBConnection, error) {
	if err := in.normalize(true); err != nil {
		return nil, err
	}
	enc, err := s.cipher.Encrypt(in.Password)
	if err != nil {
		return nil, err
	}

	conn := models.DBConnection{
		Name:        in.Name,
		Host:        in.Host,
		Port:        in.Port,
		DBName:      in.DBName,
		Username:    in.Username,
		PasswordEnc: enc,
		SSLMode:     in.SSLMode,
		CreatedBy:   createdBy,
	}
	if err := s.db.WithContext(ctx).Create(&conn).Error; err != nil {
		return nil, translateDBError(err)
	}
	return &conn, nil
}

func (s *Service) Update(ctx context.Context, id string, in ConnectionInput) (*models.DBConnection, error) {
	if err := in.normalize(false); err != nil {
		return nil, err
	}
	conn, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	conn.Name = in.Name
	conn.Host = in.Host
	conn.Port = in.Port
	conn.DBName = in.DBName
	conn.Username = in.Username
	conn.SSLMode = in.SSLMode
	if in.Password != "" {
		if conn.PasswordEnc, err = s.cipher.Encrypt(in.Password); err != nil {
			return nil, err
		}
	}
	if err := s.db.WithContext(ctx).Save(conn).Error; err != nil {
		return nil, translateDBError(err)
	}
	s.pools.Invalidate(conn.ID)
	return conn, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	res := s.db.WithContext(ctx).Delete(&models.DBConnection{}, "id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	s.pools.Invalidate(id)
	return nil
}

func (s *Service) Test(ctx context.Context, in ConnectionInput) (string, error) {
	if err := in.normalize(true); err != nil {
		return "", err
	}
	return TestConnection(ctx, in.params())
}

func (s *Service) TestSaved(ctx context.Context, id string) (string, error) {
	params, err := s.paramsFor(ctx, id)
	if err != nil {
		return "", err
	}
	return TestConnection(ctx, params)
}

func (s *Service) Pool(ctx context.Context, id string) (*pgxpool.Pool, error) {
	params, err := s.paramsFor(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.pools.Get(ctx, id, params)
}

func (s *Service) paramsFor(ctx context.Context, id string) (ConnParams, error) {
	conn, err := s.Get(ctx, id)
	if err != nil {
		return ConnParams{}, err
	}
	password, err := s.cipher.Decrypt(conn.PasswordEnc)
	if err != nil {
		return ConnParams{}, err
	}
	return ConnParams{
		Host: conn.Host, Port: conn.Port, DBName: conn.DBName,
		Username: conn.Username, Password: password, SSLMode: conn.SSLMode,
	}, nil
}

func translateDBError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicateName
	}
	return err
}

func (s *Service) CanView(ctx context.Context, userID, connectionID string) (bool, error) {
	return authz.CanView(ctx, s.db, userID, connectionID)
}

func (s *Service) CanEdit(ctx context.Context, userID, connectionID string) (bool, error) {
	return authz.CanEdit(ctx, s.db, userID, connectionID)
}

func (s *Service) ListAccessible(ctx context.Context, userID string) ([]models.DBConnection, error) {
	conns, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]models.DBConnection, 0, len(conns))
	for _, conn := range conns {
		ok, err := s.CanView(ctx, userID, conn.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			visible = append(visible, conn)
		}
	}
	return visible, nil
}
