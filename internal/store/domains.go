package store

import (
	"context"
	"database/sql"
	"errors"

	"mb/internal/asset"
	"mb/internal/money"
)

// listOrder 是列表的默认顺序:置顶优先,其次是用户拖拽出来的顺序。
const listOrder = ` ORDER BY pinned DESC, sort_order ASC, id ASC`

const domainColumns = `id, domain, registrar, expires_at, renewal_price, currency, notes,
	(exclude_cost != 0), (pinned != 0), (hidden != 0), sort_order, created_at, updated_at`

// insertDomainSQL 是唯一一条插入域名的语句,新增与备份恢复共用,避免两份 SQL 抄写不一致。
const insertDomainSQL = `INSERT INTO domains(
		domain, registrar, expires_at, renewal_price, currency, notes,
		exclude_cost, pinned, hidden, sort_order, created_at, updated_at)
	VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// execer 同时适配 *sql.DB 与 *sql.Tx。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func scanDomain(r rowScanner) (asset.Domain, error) {
	var d asset.Domain
	err := r.Scan(&d.ID, &d.Name, &d.Registrar, &d.ExpiresAt, &d.Price, &d.Currency, &d.Notes,
		&d.ExcludeCost, &d.Pinned, &d.Hidden, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt)
	d.ExpiresAt = asset.CanonicalExpiry(d.ExpiresAt)
	d.Currency = storedCurrency(d.Currency)
	return d, err
}

// storedCurrency 规范化读出的币种;历史数据里的空值按默认币种处理。
func storedCurrency(code string) string {
	if code = money.Normalize(code); code != "" {
		return code
	}
	return money.DefaultCurrency
}

func insertDomain(ctx context.Context, ex execer, d asset.Domain) (int64, error) {
	res, err := ex.ExecContext(ctx, insertDomainSQL,
		d.Name, d.Registrar, d.ExpiresAt, d.Price, d.Currency, d.Notes,
		boolInt(d.ExcludeCost), boolInt(d.Pinned), boolInt(d.Hidden), d.SortOrder, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Domains 按默认顺序返回全部域名。
func (s *Store) Domains(ctx context.Context) ([]asset.Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains`+listOrder)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanDomain)
}

// Domain 返回一条域名记录;不存在时返回 ErrNotFound。
func (s *Store) Domain(ctx context.Context, id int64) (asset.Domain, error) {
	d, err := scanDomain(s.db.QueryRowContext(ctx,
		`SELECT `+domainColumns+` FROM domains WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// CreateDomain 新增域名并排到列表末尾,返回新记录的 ID。
func (s *Store) CreateDomain(ctx context.Context, d asset.Domain) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		next, err := nextSortOrder(ctx, tx, "domains")
		if err != nil {
			return err
		}
		d.SortOrder = next
		d.CreatedAt = timestamp()
		d.UpdatedAt = d.CreatedAt
		id, err = insertDomain(ctx, tx, d)
		return err
	})
	return id, err
}

// UpdateDomain 按 d.ID 更新域名的可编辑字段;记录不存在时返回 ErrNotFound。
func (s *Store) UpdateDomain(ctx context.Context, d asset.Domain) error {
	res, err := s.db.ExecContext(ctx, `UPDATE domains SET
			domain = ?, registrar = ?, expires_at = ?, renewal_price = ?, currency = ?, notes = ?,
			exclude_cost = ?, pinned = ?, hidden = ?, updated_at = ?
		WHERE id = ?`,
		d.Name, d.Registrar, d.ExpiresAt, d.Price, d.Currency, d.Notes,
		boolInt(d.ExcludeCost), boolInt(d.Pinned), boolInt(d.Hidden), timestamp(), d.ID)
	return expectOneRow(res, err)
}

func nextSortOrder(ctx context.Context, tx *sql.Tx, table string) (int, error) {
	var next int
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM `+table).Scan(&next)
	return next, err
}

func expectOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
