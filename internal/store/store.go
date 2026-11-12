// Package store 是 SQLite 持久层:负责建表/迁移、设置项读写,以及域名、VPS、提醒日志的增删改查。
//
// 数据库只开一个连接(SQLite 单写者,省去锁竞争)。因此所有查询都必须先把结果读完、
// 关闭游标后再发起下一条语句,事务内只能使用事务句柄。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"sync"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动,无需 CGO
)

// ErrNotFound 表示要读取或更新的记录不存在。
var ErrNotFound = errors.New("store: record not found")

// Store 封装数据库连接与设置项缓存。
type Store struct {
	db *sql.DB

	mu       sync.RWMutex
	settings map[string]string
}

// Open 打开(必要时创建)数据库,执行迁移并加载设置项。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s := &Store{db: db}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if err := s.migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := s.loadSettings(ctx); err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	return nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error { return s.db.Close() }

// Ping 检查数据库是否可用。
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// dsn 生成 modernc.org/sqlite 的连接串。注意该驱动只认 _pragma=name(value) 形式的参数。
func dsn(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	return "file:" + escaped + "?_pragma=busy_timeout(5000)"
}

// inTx 在事务中执行 fn;fn 返回错误或 panic 时回滚。
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // 已提交时是空操作
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// timestamp 返回写入 created_at / updated_at 的当前时间。
func timestamp() string { return time.Now().Format(time.RFC3339) }

// rowScanner 同时适配 *sql.Row 与 *sql.Rows。
type rowScanner interface {
	Scan(dest ...any) error
}

// collect 读完并关闭 rows,把每一行交给 scan 转成 T。
func collect[T any](rows *sql.Rows, scan func(rowScanner) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
