package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdvisoryLock holds a Postgres session-level advisory lock on a single
// connection from the pool. The lock is released when Release is called.
type AdvisoryLock struct {
	conn *pgxpool.Conn
	id   int64
}

// TryAdvisoryLock attempts to acquire a Postgres advisory lock with the given
// id. It returns the lock if acquired; the caller must call Release. If the
// lock is already held by another session, it returns acquired=false and no
// error.
func (db *DB) TryAdvisoryLock(ctx context.Context, id int64) (*AdvisoryLock, bool, error) {
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire connection: %w", err)
	}

	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", id).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}

	return &AdvisoryLock{conn: conn, id: id}, true, nil
}

// Release unlocks the advisory lock and returns the connection to the pool.
func (l *AdvisoryLock) Release(ctx context.Context) error {
	if l.conn == nil {
		return nil
	}
	_, err := l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", l.id)
	l.conn.Release()
	l.conn = nil
	return err
}
