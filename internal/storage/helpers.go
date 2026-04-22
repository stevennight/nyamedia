package storage

import (
	"database/sql"
	"errors"
)

var ErrNotFound = errors.New("not found")

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ensureRowsAffected(result sql.Result, msg string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
