package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"

	"github.com/bmatei/libgo/observability/logs"
)

type sqlite3Connection struct {
	db *sql.DB
}

func NewSqlite3Connection(path string) (*sqlite3Connection, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		log.Error().Str("path", path).Err(err).Msg("Failed to open sqlite3 db")

		return nil, err
	}

	log.Info().Str("path", path).Msg("sqlite3 db opened")

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		log.Error().Str("path", path).Err(err).Msg("Failed to enable foreign keys support")

		return nil, err
	}

	return &sqlite3Connection{db: db}, nil
}

func (c *sqlite3Connection) Exec(ctx context.Context, q string, args ...interface{}) (Result, error) {
	res, err := c.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return &sqlite3Result{res}, nil
}

func (c *sqlite3Connection) Query(ctx context.Context, q string, args ...interface{}) (Rows, error) {
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return &sqlite3Rows{rows}, nil
}

func (c *sqlite3Connection) QueryRow(ctx context.Context, q string, args ...interface{}) Row {
	return &sqlite3Row{c.db.QueryRowContext(ctx, q, args...)}
}

func (c *sqlite3Connection) Begin(ctx context.Context) (Connection, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	return &sqlite3Transaction{tx, ctx, nil}, nil
}

func (c *sqlite3Connection) Close() {
	c.db.Close()
}

type sqlite3Result struct {
	res sql.Result
}

func (r sqlite3Result) RowsAffected() (int64, error) {
	return r.res.RowsAffected()
}

type sqlite3Rows struct {
	rows *sql.Rows
}

func (r sqlite3Rows) Next() bool {
	return r.rows.Next()
}

func (r sqlite3Rows) Scan(dest ...interface{}) error {
	err := r.rows.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRows
	}

	return err
}

func (r sqlite3Rows) Close() error {
	return r.rows.Close()
}

func (r sqlite3Rows) Err() error {
	return r.rows.Err()
}

type sqlite3Row struct {
	row *sql.Row
}

func (r sqlite3Row) Scan(dest ...interface{}) error {
	err := r.row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRows
	}

	return err
}

type sqlite3Transaction struct {
	tx  *sql.Tx
	ctx context.Context
	err error
}

func (tx *sqlite3Transaction) Begin(ctx context.Context) (Connection, error) {
	return tx, nil
}

func (tx *sqlite3Transaction) Exec(ctx context.Context, q string, args ...interface{}) (Result, error) {
	res, err := tx.tx.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return &sqlite3Result{res}, nil
}

func (tx *sqlite3Transaction) Query(ctx context.Context, q string, args ...interface{}) (Rows, error) {
	rows, err := tx.tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return &sqlite3Rows{rows}, nil
}

func (tx *sqlite3Transaction) QueryRow(ctx context.Context, q string, args ...interface{}) Row {
	return &sqlite3Row{tx.tx.QueryRowContext(ctx, q, args...)}
}

func (tx *sqlite3Transaction) Close() {
	logger := logs.FromContext(tx.ctx)

	if tx.err != nil {
		err := tx.tx.Rollback()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to rollback transaction")
		}
	} else {
		err := tx.tx.Commit()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to commit transaction")
		}
	}
}

func RunSqlite3Migrations(ctx context.Context, conn Connection, path string) error {
	logger := logs.FromContext(ctx).With().Str("path", path).Logger()

	file, err := os.ReadFile(path)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to read migrations file")
		return err
	}

	queries := strings.TrimSpace(string(file))
	if queries == "" {
		logger.Error().Msg("Empty migration file")
		return ErrEmptyFile
	}

	_, err = conn.Exec(ctx, queries)
	if err != nil {
		logger.Error().Err(err).Str("queries", queries).Msg("Failed to execute migrations")
		return err
	}

	return nil
}
