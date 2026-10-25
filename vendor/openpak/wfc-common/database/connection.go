package database

import (
	"context"
	"fmt"
	"openpak/wfc-common/common"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Connection struct {
	pool *pgxpool.Pool
	ctx  context.Context
}

func Start(config common.Config) Connection {
	conn := Connection{
		ctx: context.Background(),
	}

	dbString := fmt.Sprintf("postgres://%s:%s@%s/%s", config.Username, config.Password, config.DatabaseAddress, config.DatabaseName)
	dbConf, err := pgxpool.ParseConfig(dbString)
	if err != nil {
		panic(err)
	}

	conn.pool, err = pgxpool.NewWithConfig(conn.ctx, dbConf)
	if err != nil {
		panic(err)
	}
	// pgx v5 pools connect lazily; v4 connected here. Keep failing at start-up on a bad database.
	if err := conn.pool.Ping(conn.ctx); err != nil {
		panic(err)
	}

	return conn
}

func (c *Connection) Close() {
	if c != nil && c.pool != nil {
		c.pool.Close()
	}
}
