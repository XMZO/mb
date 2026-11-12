package store

import (
	"context"
	"database/sql"
	"strings"
)

// 这两个设置项关系到登录安全,备份与恢复时需要特殊对待,因此在此集中定义。
// 其余设置项的 key 由各自的使用方(fx、mailer、reminder、web ...)管理。
const (
	KeyCookieSecret = "cookie_secret"
	KeyPasswordHash = "admin_password_hash"
)

// 设置项很少、读多写少:启动时整表读入内存,之后所有读取都不碰数据库。

func (s *Store) loadSettings(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return err
	}
	type pair struct{ key, value string }
	pairs, err := collect(rows, func(r rowScanner) (pair, error) {
		var p pair
		err := r.Scan(&p.key, &p.value)
		return p, err
	})
	if err != nil {
		return err
	}

	values := make(map[string]string, len(pairs))
	for _, p := range pairs {
		values[p.key] = p.value
	}
	s.mu.Lock()
	s.settings = values
	s.mu.Unlock()
	return nil
}

// Get 返回设置项的值以及它是否存在。
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.settings[key]
	return v, ok
}

// String 返回设置项的值;不存在时返回 fallback。
func (s *Store) String(key, fallback string) string {
	if v, ok := s.Get(key); ok {
		return v
	}
	return fallback
}

// Bool 把设置项解析为布尔值;不存在或无法识别时返回 fallback。
func (s *Store) Bool(key string, fallback bool) bool {
	v, _ := s.Get(key)
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// BoolValue 返回布尔设置项的存储写法。
func BoolValue(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Settings 返回全部设置项的副本。
func (s *Store) Settings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.settings))
	for k, v := range s.settings {
		out[k] = v
	}
	return out
}

// Set 写入一个设置项。
func (s *Store) Set(ctx context.Context, key, value string) error {
	return s.SetMany(ctx, map[string]string{key: value})
}

// SetMany 在一个事务里写入多个设置项:要么全部生效,要么全部不变。
func (s *Store) SetMany(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		return upsertSettings(ctx, tx, values)
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	for k, v := range values {
		s.settings[k] = v
	}
	s.mu.Unlock()
	return nil
}

func upsertSettings(ctx context.Context, tx *sql.Tx, values map[string]string) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for k, v := range values {
		if _, err := stmt.ExecContext(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}
