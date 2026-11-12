package store

import (
	"context"
	"database/sql"
	"errors"

	"mb/internal/asset"
)

const vpsColumns = `id, vendor, ip, specs, region, cores, memory_gb, disks_json,
	traffic_tb, (traffic_set != 0), traffic_direction, bandwidth_value, bandwidth_unit,
	expires_at, renewal_price, currency, notes,
	(exclude_cost != 0), billing_months, (pinned != 0), (hidden != 0), sort_order, created_at, updated_at`

// insertVPSSQL 是唯一一条插入 VPS 的语句,新增与备份恢复共用。
const insertVPSSQL = `INSERT INTO vps(
		vendor, ip, specs, region, cores, memory_gb, disks_json,
		traffic_tb, traffic_set, traffic_direction, bandwidth_value, bandwidth_unit,
		expires_at, renewal_price, currency, notes,
		exclude_cost, billing_months, pinned, hidden, sort_order, created_at, updated_at)
	VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func scanVPS(r rowScanner) (asset.VPS, error) {
	var v asset.VPS
	var disksJSON string
	err := r.Scan(&v.ID, &v.Vendor, &v.IP, &v.Specs, &v.Region, &v.Cores, &v.MemoryGB, &disksJSON,
		&v.TrafficTB, &v.TrafficSet, &v.TrafficDir, &v.BandwidthValue, &v.BandwidthUnit,
		&v.ExpiresAt, &v.Price, &v.Currency, &v.Notes,
		&v.ExcludeCost, &v.BillingMonths, &v.Pinned, &v.Hidden, &v.SortOrder, &v.CreatedAt, &v.UpdatedAt)

	v.Disks = asset.ParseDisksJSON(disksJSON)
	v.TrafficDir = asset.NormalizeTrafficDir(v.TrafficDir)
	if !v.TrafficSet {
		v.TrafficTB, v.TrafficDir = 0, ""
	}
	v.BandwidthUnit = asset.NormalizeBandwidthUnit(v.BandwidthUnit)
	if v.BillingMonths <= 0 {
		v.BillingMonths = asset.DefaultBillingMonths
	}
	v.ExpiresAt = asset.CanonicalExpiry(v.ExpiresAt)
	v.Currency = storedCurrency(v.Currency)
	return v, err
}

// vpsValues 返回 VPS 的可编辑字段,顺序与 insertVPSSQL / UpdateVPS 的前 20 个占位符一致。
func vpsValues(v asset.VPS) []any {
	return []any{
		v.Vendor, v.IP, v.Specs, v.Region, v.Cores, v.MemoryGB, asset.DisksJSON(v.Disks),
		v.TrafficTB, boolInt(v.TrafficSet), v.TrafficDir, v.BandwidthValue, v.BandwidthUnit,
		v.ExpiresAt, v.Price, v.Currency, v.Notes,
		boolInt(v.ExcludeCost), v.BillingMonths, boolInt(v.Pinned), boolInt(v.Hidden),
	}
}

func insertVPS(ctx context.Context, ex execer, v asset.VPS) (int64, error) {
	args := append(vpsValues(v), v.SortOrder, v.CreatedAt, v.UpdatedAt)
	res, err := ex.ExecContext(ctx, insertVPSSQL, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// VPSList 按默认顺序返回全部 VPS。
func (s *Store) VPSList(ctx context.Context) ([]asset.VPS, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+vpsColumns+` FROM vps`+listOrder)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanVPS)
}

// VPS 返回一条 VPS 记录;不存在时返回 ErrNotFound。
func (s *Store) VPS(ctx context.Context, id int64) (asset.VPS, error) {
	v, err := scanVPS(s.db.QueryRowContext(ctx, `SELECT `+vpsColumns+` FROM vps WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// CreateVPS 新增 VPS 并排到列表末尾,返回新记录的 ID。
func (s *Store) CreateVPS(ctx context.Context, v asset.VPS) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		next, err := nextSortOrder(ctx, tx, "vps")
		if err != nil {
			return err
		}
		v.SortOrder = next
		v.CreatedAt = timestamp()
		v.UpdatedAt = v.CreatedAt
		id, err = insertVPS(ctx, tx, v)
		return err
	})
	return id, err
}

// UpdateVPS 按 v.ID 更新 VPS 的可编辑字段;记录不存在时返回 ErrNotFound。
func (s *Store) UpdateVPS(ctx context.Context, v asset.VPS) error {
	args := append(vpsValues(v), timestamp(), v.ID)
	res, err := s.db.ExecContext(ctx, `UPDATE vps SET
			vendor = ?, ip = ?, specs = ?, region = ?, cores = ?, memory_gb = ?, disks_json = ?,
			traffic_tb = ?, traffic_set = ?, traffic_direction = ?, bandwidth_value = ?, bandwidth_unit = ?,
			expires_at = ?, renewal_price = ?, currency = ?, notes = ?,
			exclude_cost = ?, billing_months = ?, pinned = ?, hidden = ?, updated_at = ?
		WHERE id = ?`, args...)
	return expectOneRow(res, err)
}
