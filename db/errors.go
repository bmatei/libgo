package db

import "errors"

var (
	ErrNoRows = errors.New("no rows in result set")
	ErrEmptyFile = errors.New("empty migration file")
)
