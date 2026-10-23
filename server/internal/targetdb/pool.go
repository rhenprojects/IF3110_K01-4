package targetdb

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	poolMaxConns   = 5
	connectTimeout = (ConnectTimeoutSeconds + 2) * time.Second
)

type PoolManager struct {
	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

func NewPoolManager() *PoolManager {
	return &PoolManager{pools: make(map[string]*pgxpool.Pool)}
}

func (m *PoolManager) Get(ctx context.Context, id string, params ConnParams) (*pgxpool.Pool, error) {
	m.mu.Lock()
	pool, ok := m.pools[id]
	m.mu.Unlock()
	if ok {
		return pool, nil
	}

	cfg, err := pgxpool.ParseConfig(params.DSN())
	if err != nil {
		return nil, classifyConnError(err)
	}
	cfg.MaxConns = poolMaxConns

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, classifyConnError(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, classifyConnError(err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.pools[id]; ok {
		go pool.Close()
		return existing, nil
	}
	m.pools[id] = pool
	return pool, nil
}

func (m *PoolManager) Invalidate(id string) {
	m.mu.Lock()
	pool, ok := m.pools[id]
	delete(m.pools, id)
	m.mu.Unlock()
	if ok {
		go pool.Close()
	}
}

func (m *PoolManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, pool := range m.pools {
		pool.Close()
		delete(m.pools, id)
	}
}

func TestConnection(ctx context.Context, params ConnParams) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, params.DSN())
	if err != nil {
		return "", classifyConnError(err)
	}
	defer conn.Close(context.Background())

	var version string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		return "", classifyConnError(err)
	}
	return version, nil
}
