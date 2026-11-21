package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

// lockNamespace memisahkan advisory lock milik platform dari lock lain di database yang sama
const lockNamespace int32 = 0x4442 // "DB"

// Locker memberi lock "satu migration per database target" yang berlaku lintas goroutine, worker, dan proses.
type Locker struct {
	db *sql.DB
}

func NewLocker(db *sql.DB) *Locker { return &Locker{db: db} }

// TryLock tidak menunggu: jika database target sedang dipakai migration lain, ok bernilai false.
func (l *Locker) TryLock(ctx context.Context, connectionID string) (release func(), ok bool, err error) {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock($1, hashtext($2))`, lockNamespace, connectionID).Scan(&got); err != nil {
		discard(conn)
		return nil, false, err
	}
	if !got {
		_ = conn.Close()
		return nil, false, nil
	}
	return func() {
		var unlocked bool
		err := conn.QueryRowContext(context.Background(),
			`SELECT pg_advisory_unlock($1, hashtext($2))`, lockNamespace, connectionID).Scan(&unlocked)
		if err != nil || !unlocked {

			discard(conn)
			return
			
		}
		_ = conn.Close()
	}, true, nil
}

// discard menutup koneksi fisik alih-alih mengembalikannya ke pool.
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
