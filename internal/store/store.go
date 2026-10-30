// Package store 封装 SQLite 连接、迁移和 sqlc 生成的查询。
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/eagle/proxystack-web/internal/store/dbq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound 表示查询的记录不存在。
var ErrNotFound = errors.New("not found")

// Store 持有数据库连接和查询对象。
type Store struct {
	DB *sql.DB
	Q  *dbq.Queries
}

// Open 打开数据库并设置 WAL、外键和忙等待。不执行迁移。
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	// 数据库含凭据：收紧较早创建、权限过宽的数据库文件（WAL/SHM 由 SQLite 按主文件权限创建）。
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil && info.Mode().Perm()&0o027 != 0 {
			_ = os.Chmod(path+suffix, info.Mode().Perm()&^0o027)
		}
	}
	return &Store{DB: db, Q: dbq.New(db)}, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error { return s.DB.Close() }

func newProvider(db *sql.DB) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, db, sub)
}

// Migrate 执行全部未应用的迁移。
func (s *Store) Migrate(ctx context.Context) error {
	provider, err := newProvider(s.DB)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return nil
}

// MigrationStatus 返回当前数据库版本和程序内置的最新版本。
func (s *Store) MigrationStatus(ctx context.Context) (current, latest int64, err error) {
	provider, err := newProvider(s.DB)
	if err != nil {
		return 0, 0, err
	}
	current, err = provider.GetDBVersion(ctx)
	if err != nil {
		return 0, 0, err
	}
	sources := provider.ListSources()
	if len(sources) > 0 {
		latest = sources[len(sources)-1].Version
	}
	return current, latest, nil
}

// InTx 在一个事务内执行 fn，fn 返回错误时回滚。
func (s *Store) InTx(ctx context.Context, fn func(q *dbq.Queries) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// GetJSON 读取一个 JSON 设置项到 out；不存在时返回 ErrNotFound。
func GetJSON(ctx context.Context, q *dbq.Queries, key string, out any) error {
	raw, err := q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

// PutJSON 把 value 编码为 JSON 写入设置项。
func PutJSON(ctx context.Context, q *dbq.Queries, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return q.PutSetting(ctx, dbq.PutSettingParams{Key: key, Value: string(raw)})
}
