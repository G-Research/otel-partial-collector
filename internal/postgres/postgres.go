package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool

	// ephemeral when in tx
	tx pgx.Tx
}

func NewDB(ctx context.Context, conn string) (*DB, error) {
	pool, err := pgxpool.New(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create new pgx pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping the database: %w", err)
	}

	return &DB{
		pool: pool,
	}, nil
}

func (db *DB) Close() error {
	if db.pool == nil {
		return errors.New("pool is nil")
	}
	db.pool.Close()
	return nil
}

func (db *DB) Transact(ctx context.Context, opts pgx.TxOptions, f func(ctx context.Context, db *DB) error) error {
	switch opts.IsoLevel {
	case pgx.Serializable, pgx.RepeatableRead:
		return db.transactWithRetry(ctx, opts, f)
	default:
		return db.transact(ctx, opts, f)
	}
}

func (db *DB) transactWithRetry(ctx context.Context, opts pgx.TxOptions, f func(ctx context.Context, db *DB) error) error {
	var errs []error
	for range 5 {
		err := db.transact(ctx, opts, f)
		if err == nil {
			return nil
		}

		errs = append(errs, err)
		if !isSerializationError(err) {
			return errors.Join(errs...)
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("transaction failed after 5 retries: %w", errors.Join(errs...))
}

func isSerializationError(err error) bool {
	var gerr *pgconn.PgError
	if errors.As(err, &gerr) && gerr.Code == "40001" {
		return true
	}
	return false
}

func (db *DB) transact(ctx context.Context, opts pgx.TxOptions, f func(ctx context.Context, db *DB) error) error {
	var tx pgx.Tx
	var err error
	if db.tx != nil {
		tx, err = db.tx.Begin(ctx)
	} else {
		tx, err = db.pool.BeginTx(ctx, opts)
	}

	if err != nil {
		return err
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	err = f(ctx, &DB{tx: tx})
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (db *DB) Begin(ctx context.Context) (pgx.Tx, error) {
	if db.tx != nil {
		return db.tx.Begin(ctx)
	}
	return db.pool.Begin(ctx)
}

func (db *DB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if db.tx != nil {
		return db.tx.Exec(ctx, sql, arguments...)
	}
	return db.pool.Exec(ctx, sql, arguments...)
}

func (db *DB) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	if db.tx != nil {
		return db.tx.Query(ctx, sql, arguments...)
	}
	return db.pool.Query(ctx, sql, arguments...)
}

func (db *DB) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	if db.tx != nil {
		return db.tx.QueryRow(ctx, sql, arguments...)
	}
	return db.pool.QueryRow(ctx, sql, arguments...)
}
