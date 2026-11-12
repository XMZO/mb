package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"mb/internal/asset"
)

// 本文件是域名与 VPS 共用的批量操作:两张表在这些列上结构相同,只有表名不同。

// tables 是资产类型到表名的白名单;表名只会来自这里,不会拼接外部输入。
var tables = map[asset.Kind]string{
	asset.KindDomain: "domains",
	asset.KindVPS:    "vps",
}

func tableOf(kind asset.Kind) (string, error) {
	table, ok := tables[kind]
	if !ok {
		return "", fmt.Errorf("store: unknown asset kind %q", kind)
	}
	return table, nil
}

// Flag 是资产上可批量开关的布尔列。
type Flag string

const (
	FlagPinned      Flag = "pinned"
	FlagHidden      Flag = "hidden"
	FlagExcludeCost Flag = "exclude_cost"
)

// idList 生成 IN 子句的占位符与参数。
func idList(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

// updateByIDs 执行 UPDATE <table> SET <column> = ?, updated_at = ? WHERE id IN (...)。
func (s *Store) updateByIDs(ctx context.Context, kind asset.Kind, ids []int64, column string, value any) error {
	table, err := tableOf(kind)
	if err != nil || len(ids) == 0 {
		return err
	}
	in, idArgs := idList(ids)
	args := append([]any{value, timestamp()}, idArgs...)
	_, err = s.db.ExecContext(ctx,
		`UPDATE `+table+` SET `+column+` = ?, updated_at = ? WHERE id IN `+in, args...)
	return err
}

// DeleteAssets 删除指定的记录。
func (s *Store) DeleteAssets(ctx context.Context, kind asset.Kind, ids []int64) error {
	table, err := tableOf(kind)
	if err != nil || len(ids) == 0 {
		return err
	}
	in, args := idList(ids)
	_, err = s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id IN `+in, args...)
	return err
}

// SetFlag 批量设置置顶 / 隐藏 / 不计成本。
func (s *Store) SetFlag(ctx context.Context, kind asset.Kind, ids []int64, flag Flag, on bool) error {
	switch flag {
	case FlagPinned, FlagHidden, FlagExcludeCost:
		return s.updateByIDs(ctx, kind, ids, string(flag), boolInt(on))
	}
	return fmt.Errorf("store: unknown flag %q", flag)
}

// SetExpiry 把指定记录的到期日统一设为 expires(需已通过 asset.ParseExpiry 校验)。
func (s *Store) SetExpiry(ctx context.Context, kind asset.Kind, ids []int64, expires string) error {
	return s.updateByIDs(ctx, kind, ids, "expires_at", expires)
}

// SetBillingMonths 批量设置 VPS 的付费周期。
func (s *Store) SetBillingMonths(ctx context.Context, ids []int64, months int) error {
	return s.updateByIDs(ctx, asset.KindVPS, ids, "billing_months", months)
}

// ExtendExpiry 把指定记录各自的到期日顺延 months 个月;永久或日期无效的记录保持不变。
func (s *Store) ExtendExpiry(ctx context.Context, kind asset.Kind, ids []int64, months int) error {
	table, err := tableOf(kind)
	if err != nil || len(ids) == 0 {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		in, args := idList(ids)
		rows, err := tx.QueryContext(ctx, `SELECT id, expires_at FROM `+table+` WHERE id IN `+in, args...)
		if err != nil {
			return err
		}
		type item struct {
			id      int64
			expires string
		}
		items, err := collect(rows, func(r rowScanner) (item, error) {
			var it item
			err := r.Scan(&it.id, &it.expires)
			return it, err
		})
		if err != nil {
			return err
		}

		now := timestamp()
		for _, it := range items {
			current := asset.CanonicalExpiry(it.expires)
			if asset.IsPermanent(current) {
				continue
			}
			next, err := asset.AddMonths(current, months)
			if err != nil {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE `+table+` SET expires_at = ?, updated_at = ? WHERE id = ?`, next, now, it.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Reorder 按 ids 给出的先后顺序重排列表;ids 中没有提到的记录保持原有的相对顺序,排在后面。
func (s *Store) Reorder(ctx context.Context, kind asset.Kind, ids []int64) error {
	table, err := tableOf(kind)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM `+table+listOrder)
		if err != nil {
			return err
		}
		existing, err := collect(rows, scanID)
		if err != nil {
			return err
		}

		known := make(map[int64]bool, len(existing))
		for _, id := range existing {
			known[id] = true
		}
		placed := make(map[int64]bool, len(existing))
		order := make([]int64, 0, len(existing))
		for _, id := range append(append([]int64{}, ids...), existing...) {
			if known[id] && !placed[id] {
				placed[id] = true
				order = append(order, id)
			}
		}

		stmt, err := tx.PrepareContext(ctx, `UPDATE `+table+` SET sort_order = ? WHERE id = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i, id := range order {
			if _, err := stmt.ExecContext(ctx, i+1, id); err != nil {
				return err
			}
		}
		return nil
	})
}
