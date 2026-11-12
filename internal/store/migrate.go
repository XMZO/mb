package store

import (
	"context"
	"fmt"

	"mb/internal/asset"
)

// schemaVersion 记录在 PRAGMA user_version 中。0 表示全新数据库,或由旧版程序
// (没有版本号的年代)创建的数据库,两者都需要走一遍 upgradeLegacy。
const schemaVersion = 1

// schema 是当前版本的完整表结构;语句全部幂等,每次启动都会执行。
var schema = []string{
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS domains (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		domain        TEXT NOT NULL,
		registrar     TEXT NOT NULL DEFAULT '',
		expires_at    TEXT NOT NULL,
		renewal_price REAL NOT NULL DEFAULT 0,
		currency      TEXT NOT NULL DEFAULT 'CNY',
		notes         TEXT NOT NULL DEFAULT '',
		pinned        INTEGER NOT NULL DEFAULT 0,
		hidden        INTEGER NOT NULL DEFAULT 0,
		exclude_cost  INTEGER NOT NULL DEFAULT 0,
		sort_order    INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_domains_expires ON domains(expires_at)`,
	`CREATE TABLE IF NOT EXISTS vps (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		vendor            TEXT NOT NULL,
		ip                TEXT NOT NULL DEFAULT '',
		specs             TEXT NOT NULL DEFAULT '',
		region            TEXT NOT NULL DEFAULT '',
		cores             INTEGER NOT NULL DEFAULT 0,
		memory_gb         REAL NOT NULL DEFAULT 0,
		disks_json        TEXT NOT NULL DEFAULT '',
		traffic_tb        REAL NOT NULL DEFAULT 0,
		traffic_set       INTEGER NOT NULL DEFAULT 0,
		traffic_direction TEXT NOT NULL DEFAULT '',
		bandwidth_value   REAL NOT NULL DEFAULT 0,
		bandwidth_unit    TEXT NOT NULL DEFAULT 'Mbps',
		expires_at        TEXT NOT NULL,
		renewal_price     REAL NOT NULL DEFAULT 0,
		currency          TEXT NOT NULL DEFAULT 'CNY',
		notes             TEXT NOT NULL DEFAULT '',
		pinned            INTEGER NOT NULL DEFAULT 0,
		hidden            INTEGER NOT NULL DEFAULT 0,
		exclude_cost      INTEGER NOT NULL DEFAULT 0,
		billing_months    INTEGER NOT NULL DEFAULT 12,
		sort_order        INTEGER NOT NULL DEFAULT 0,
		created_at        TEXT NOT NULL,
		updated_at        TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_vps_expires ON vps(expires_at)`,
	`CREATE TABLE IF NOT EXISTS reminder_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		item_type   TEXT NOT NULL,
		item_id     INTEGER NOT NULL,
		days_before INTEGER NOT NULL,
		sent_date   TEXT NOT NULL,
		created_at  TEXT NOT NULL,
		UNIQUE(item_type, item_id, days_before, sent_date)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_reminder_log_date ON reminder_log(sent_date)`,
}

// legacyColumns 是早期版本的表里可能缺少的列。
var legacyColumns = []struct{ table, column, definition string }{
	{"domains", "sort_order", "INTEGER NOT NULL DEFAULT 0"},
	{"domains", "exclude_cost", "INTEGER NOT NULL DEFAULT 0"},
	{"domains", "pinned", "INTEGER NOT NULL DEFAULT 0"},
	{"domains", "hidden", "INTEGER NOT NULL DEFAULT 0"},
	{"vps", "region", "TEXT NOT NULL DEFAULT ''"},
	{"vps", "cores", "INTEGER NOT NULL DEFAULT 0"},
	{"vps", "memory_gb", "REAL NOT NULL DEFAULT 0"},
	{"vps", "disks_json", "TEXT NOT NULL DEFAULT ''"},
	{"vps", "traffic_tb", "REAL NOT NULL DEFAULT 0"},
	{"vps", "traffic_set", "INTEGER NOT NULL DEFAULT 0"},
	{"vps", "traffic_direction", "TEXT NOT NULL DEFAULT ''"},
	{"vps", "bandwidth_value", "REAL NOT NULL DEFAULT 0"},
	{"vps", "bandwidth_unit", "TEXT NOT NULL DEFAULT 'Mbps'"},
	{"vps", "exclude_cost", "INTEGER NOT NULL DEFAULT 0"},
	{"vps", "billing_months", "INTEGER NOT NULL DEFAULT 12"},
	{"vps", "sort_order", "INTEGER NOT NULL DEFAULT 0"},
	{"vps", "pinned", "INTEGER NOT NULL DEFAULT 0"},
	{"vps", "hidden", "INTEGER NOT NULL DEFAULT 0"},
}

func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= schemaVersion {
		return nil
	}
	if err := s.upgradeLegacy(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
	return err
}

// upgradeLegacy 把旧版数据库整理成当前结构。每一步都幂等,对全新数据库是空操作。
//
// 旧表里多余的列(disk_gb、disk2_gb、bandwidth_mbps 等)会原样保留:它们都有默认值,
// 不影响新代码写入,也让数据库仍能被旧版程序打开。
func (s *Store) upgradeLegacy(ctx context.Context) error {
	for _, c := range legacyColumns {
		exists, err := s.hasColumn(ctx, c.table, c.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.column, c.definition)
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if err := s.foldLegacyDisks(ctx); err != nil {
		return err
	}
	if exists, err := s.hasColumn(ctx, "vps", "bandwidth_mbps"); err != nil {
		return err
	} else if exists {
		// 最早的版本只有以 Mbps 为单位的整数带宽。
		if _, err := s.db.ExecContext(ctx, `UPDATE vps SET bandwidth_value = bandwidth_mbps, bandwidth_unit = 'Mbps'
			WHERE bandwidth_value = 0 AND bandwidth_mbps > 0`); err != nil {
			return err
		}
	}

	fixes := []string{
		`UPDATE vps SET traffic_set = 1 WHERE traffic_set = 0 AND traffic_tb > 0`,
		`UPDATE vps SET billing_months = 12 WHERE billing_months <= 0`,
	}
	for _, table := range []string{"domains", "vps"} {
		fixes = append(fixes,
			`UPDATE `+table+` SET expires_at = trim(expires_at) WHERE expires_at != trim(expires_at)`,
			`UPDATE `+table+` SET expires_at = '`+asset.Permanent+`'
				WHERE lower(expires_at) IN ('永久', '无限期', '长期', 'permanent', 'forever', 'lifetime')`,
		)
	}
	for _, stmt := range fixes {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	for _, table := range []string{"domains", "vps"} {
		if err := s.backfillSortOrder(ctx, table); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) hasColumn(ctx context.Context, table, column string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	return n > 0, err
}

// foldLegacyDisks 把旧版的"至多两块硬盘"四个列合并进 disks_json。
func (s *Store) foldLegacyDisks(ctx context.Context) error {
	exists, err := s.hasColumn(ctx, "vps", "disk_gb")
	if err != nil || !exists {
		return err
	}

	type legacy struct {
		id    int64
		disks []asset.Disk
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, disk_gb, disk_type, disk2_gb, disk2_type FROM vps
		WHERE trim(disks_json) = '' AND (disk_gb > 0 OR disk2_gb > 0)`)
	if err != nil {
		return err
	}
	pending, err := collect(rows, func(r rowScanner) (legacy, error) {
		var l legacy
		var size1, size2 int
		var type1, type2 string
		err := r.Scan(&l.id, &size1, &type1, &size2, &type2)
		l.disks = []asset.Disk{{SizeGB: size1, Type: type1}, {SizeGB: size2, Type: type2}}
		return l, err
	})
	if err != nil {
		return err
	}
	for _, l := range pending {
		if _, err := s.db.ExecContext(ctx, `UPDATE vps SET disks_json = ? WHERE id = ?`,
			asset.DisksJSON(l.disks), l.id); err != nil {
			return err
		}
	}
	return nil
}

// backfillSortOrder 给还没有排序号的旧记录补上序号,排在已有记录之后。
func (s *Store) backfillSortOrder(ctx context.Context, table string) error {
	var next int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sort_order), 0) FROM `+table).Scan(&next); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM `+table+` WHERE sort_order <= 0 ORDER BY expires_at ASC, id ASC`)
	if err != nil {
		return err
	}
	ids, err := collect(rows, scanID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		next++
		if _, err := s.db.ExecContext(ctx,
			`UPDATE `+table+` SET sort_order = ? WHERE id = ?`, next, id); err != nil {
			return err
		}
	}
	return nil
}

func scanID(r rowScanner) (int64, error) {
	var id int64
	err := r.Scan(&id)
	return id, err
}
