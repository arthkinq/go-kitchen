package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arthkinq/go-kitchen/internal/domain"
)

type txKeyType struct{}

var txKey = txKeyType{}

// DBExecutor generalizes operations executed on either a pgxpool.Pool or pgx.Tx.
type DBExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// TxManager manages database transactions with context propagation.
type TxManager struct {
	pool *pgxpool.Pool
}

// NewTxManager creates a new TxManager instance.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// WithinTransaction executes fn inside a database transaction with context propagation.
func (m *TxManager) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		_ = tx.Rollback(context.Background())
	}()

	txCtx := context.WithValue(ctx, txKey, tx)
	if err := fn(txCtx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// getExecutor returns either the current transaction from context or the root connection pool.
func getExecutor(ctx context.Context, pool *pgxpool.Pool) DBExecutor {
	if tx, ok := ctx.Value(txKey).(pgx.Tx); ok {
		return tx
	}
	return pool
}

// mapPgError maps PostgreSQL constraint violations to typed domain errors.
//
// The three codes below the conflicts are the ways a value each validator accepted on its own can
// still be refused by the column it was headed for, because what reaches the database is a product
// or a sum: a dish priced near the top of int64 with two of it in the basket overflows the total
// and breaks CHECK (total_price_cents >= 0); a stock near the top of int4 overflows when a
// cancellation returns it; a NUL written as an escape inside a JSON string is well-formed to
// encoding/json and valid UTF-8 to Go, but a C string cannot carry it.
//
// Every one of them starts as something a client sent, so every one of them is a 400. Left
// unmapped they fell through to the default branch and were answered with a 500 - a client
// mistake landing in the 5xx alerting, which is exactly what this file exists to prevent. The
// wrapped text stays in the log; respondError sends the client its own fixed message.
func mapPgError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return fmt.Errorf("%w: %s", domain.ErrConflict, pgErr.Detail)
		case "23503": // foreign_key_violation
			return fmt.Errorf("%w: %s", domain.ErrConflict, pgErr.Detail)
		case "22021": // character_not_in_repertoire - invalid UTF-8 or an embedded NUL
			return fmt.Errorf("%w: %s", domain.ErrInvalidInput, pgErr.Message)
		case "22003": // numeric_value_out_of_range - wider than the int4 column
			return fmt.Errorf("%w: %s", domain.ErrInvalidInput, pgErr.Message)
		case "23514": // check_violation
			return fmt.Errorf("%w: %s", domain.ErrInvalidInput, pgErr.ConstraintName)
		}
	}
	return err
}
