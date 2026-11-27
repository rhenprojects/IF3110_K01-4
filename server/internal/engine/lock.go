package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

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
	key := "dbmp:" + connectionID
	if err := conn.QueryRowContext(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&got); err != nil {
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
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).Scan(&unlocked)
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
