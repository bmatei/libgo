package db

import (
	"context"
	"errors"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"

	"github.com/bmatei/libgo/observability/logs"
)

type postgresConnection struct {
	pool *pgxpool.Pool
}

func NewPostgresPool(url string) (*postgresConnection, error) {
	conn, err := pgxpool.New(context.Background(), url)
	if err != nil {
		return nil, err
	}

	return &postgresConnection{conn}, nil
}

func (c *postgresConnection) Exec(ctx context.Context, q string, args ...interface{}) (Result, error) {
	tag, err := c.pool.Exec(ctx, q, args...)
	return &postgresResult{tag}, err
}

func (c *postgresConnection) Query(ctx context.Context, q string, args ...interface{}) (Rows, error) {
	rows, err := c.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return &postgresRows{rows}, nil
}

func (c *postgresConnection) QueryRow(ctx context.Context, q string, args ...interface{}) Row {
	return &postgresRow{c.pool.QueryRow(ctx, q, args...)}
}

func (c *postgresConnection) Begin(ctx context.Context) (Connection, error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return &postgresTransaction{tx, ctx, nil}, nil
}

func (c *postgresConnection) Close() {
	c.pool.Close()
}

type postgresResult struct {
	tag pgconn.CommandTag
}

func (r postgresResult) RowsAffected() (int64, error) {
	return int64(r.tag.RowsAffected()), nil
}

type postgresRows struct {
	rows pgx.Rows
}

func (r postgresRows) Next() bool {
	return r.rows.Next()
}

func (r postgresRows) Scan(dest ...interface{}) error {
	err := r.rows.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRows
	}

	return err
}

func (r postgresRows) Close() error {
	r.rows.Close()
	return nil
}

func (r postgresRows) Err() error {
	return r.rows.Err()
}

type postgresRow struct {
	row pgx.Row
}

func (r postgresRow) Scan(dest ...interface{}) error {
	err := r.row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoRows
	}

	return err
}

type postgresTransaction struct {
	tx  pgx.Tx
	ctx context.Context
	err error
}

func (tx *postgresTransaction) Begin(ctx context.Context) (Connection, error) {
	return tx, nil
}

func (tx *postgresTransaction) Close() {
	logger := logs.FromContext(tx.ctx)

	if tx.err != nil {
		err := tx.tx.Rollback(tx.ctx)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to rollback transaction")
		}
	} else {
		err := tx.tx.Commit(tx.ctx)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to commit transaction")
		}
	}
}

func (tx *postgresTransaction) Exec(ctx context.Context, q string, args ...interface{}) (Result, error) {
	tag, err := tx.tx.Exec(ctx, q, args...)
	return &postgresResult{tag}, err
}

func (tx *postgresTransaction) Query(ctx context.Context, q string, args ...interface{}) (Rows, error) {
	rows, err := tx.tx.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	return &postgresRows{rows}, nil
}

func (tx *postgresTransaction) QueryRow(ctx context.Context, q string, args ...interface{}) Row {
	return &postgresRow{tx.tx.QueryRow(ctx, q, args...)}
}

func RunPostgresMigrations(ctx context.Context, c *postgresConnection) error {
	logger := logs.FromContext(ctx)

	conn, err := c.pool.Acquire(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to acquire database connection")
		return err
	}
	defer conn.Release()

	migrator, err := migrate.NewMigrator(ctx, conn.Conn(), "schema_version")
	if err != nil {
		logger.Error().Err(err).Msg("Failed to allocate migrator")
		return err
	}

	migrationPath := "db/migrations"
	migrationFS := os.DirFS(migrationPath)
	err = migrator.LoadMigrations(migrationFS)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to load migrations")
		return err
	}

	currentVersion, err := migrator.GetCurrentVersion(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to get current migration version")
		return err
	}

	logger.Info().Int32("to", currentVersion).Msg("Migrating")

	err = migrator.Migrate(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to migrate")
		return err
	}

	newVersion, err := migrator.GetCurrentVersion(ctx)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to get new migration version")
		return err
	}

	logger.Info().
		Int32("from", currentVersion).
		Int32("to", newVersion).
		Msg("Migration success")

	return nil
}
