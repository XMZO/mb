package store

import (
	"context"
	"database/sql"

	"mb/internal/asset"
)

// ReplaceAll 用给定的数据整体替换现有的域名与 VPS(备份恢复用)。
//
// settings 为 nil 时保留现有设置;否则设置项也整体替换为 settings。
// 调用方负责校验数据,并为每条记录填好 SortOrder / CreatedAt / UpdatedAt。
// 整个过程在一个事务内完成,任何一步失败都不会改动现有数据。
func (s *Store) ReplaceAll(ctx context.Context, domains []asset.Domain, vps []asset.VPS, settings map[string]string) error {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// 记录的 ID 会重新分配,旧的提醒日志不再对应得上,一并清空。
		for _, table := range []string{"domains", "vps", "reminder_log"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
				return err
			}
		}
		for _, d := range domains {
			if _, err := insertDomain(ctx, tx, d); err != nil {
				return err
			}
		}
		for _, v := range vps {
			if _, err := insertVPS(ctx, tx, v); err != nil {
				return err
			}
		}
		if settings == nil {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings`); err != nil {
			return err
		}
		return upsertSettings(ctx, tx, settings)
	})
	if err != nil || settings == nil {
		return err
	}
	return s.loadSettings(ctx)
}
