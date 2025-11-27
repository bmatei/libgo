package db

import (
	"context"
)

type CommandTag interface {
	String() string
}

type Result interface {
	RowsAffected() (int64, error)
}

type Rows interface {
	Next() bool
	Scan(...interface{}) error
	Close() error
	Err() error
}

type Row interface {
	Scan(...interface{}) error
}

type Connection interface {
	Exec(ctx context.Context, query string, args ...interface{}) (Result, error)
	Query(ctx context.Context, query string, args ...interface{}) (Rows, error)
	QueryRow(ctx context.Context, query string, args ...interface{}) Row
	Begin(ctx context.Context) (Connection, error)
	Close()
}
