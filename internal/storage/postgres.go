package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

const postgresDriverName = "pgx-rebind"

var registerPostgresDriverOnce sync.Once

func OpenPostgres(databaseURL string) (*sql.DB, error) {
	registerPostgresDriverOnce.Do(func() {
		sql.Register(postgresDriverName, rebindDriver{driver: stdlib.GetDefaultDriver()})
	})

	db, err := sql.Open(postgresDriverName, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}

type rebindDriver struct {
	driver driver.Driver
}

func (d rebindDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.driver.Open(name)
	if err != nil {
		return nil, err
	}
	return rebindConn{Conn: conn}, nil
}

type rebindConn struct {
	driver.Conn
}

func (c rebindConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(rebindPostgres(query))
}

func (c rebindConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, rebindPostgres(query))
	}
	return c.Prepare(query)
}

func (c rebindConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, rebindPostgres(query), args)
	}
	return nil, driver.ErrSkip
}

func (c rebindConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, rebindPostgres(query), args)
	}
	return nil, driver.ErrSkip
}

func rebindPostgres(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var builder strings.Builder
	builder.Grow(len(query) + 8)
	placeholder := 1
	for _, char := range query {
		if char == '?' {
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(placeholder))
			placeholder++
			continue
		}
		builder.WriteRune(char)
	}
	return builder.String()
}
