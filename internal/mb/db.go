package mb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"strconv"
	"strings"
	"time"
)

func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// validateWriteStatements 预编译关键写语句,列数与占位符数不一致会立即失败,
// 避免此类错位(参见 P0-0)拖到运行时导入/写入才暴露。
func validateWriteStatements(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, q := range []string{insertDomainSQL, insertVPSSQL} {
		stmt, err := db.PrepareContext(ctx, q)
		if err != nil {
			return fmt.Errorf("write-statement self-check failed: %w", err)
		}
		if cerr := stmt.Close(); cerr != nil {
			return cerr
		}
	}
	return nil
}
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL,
			registrar TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL,
			renewal_price REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'CNY',
			notes TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL DEFAULT 0,
			hidden INTEGER NOT NULL DEFAULT 0,
			exclude_cost INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_domains_expires ON domains(expires_at);`,
		`CREATE TABLE IF NOT EXISTS vps (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			vendor TEXT NOT NULL,
			ip TEXT NOT NULL DEFAULT '',
			specs TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			cores INTEGER NOT NULL DEFAULT 0,
			memory_gb REAL NOT NULL DEFAULT 0,
			disk_gb INTEGER NOT NULL DEFAULT 0,
			disk_type TEXT NOT NULL DEFAULT '',
			disk2_gb INTEGER NOT NULL DEFAULT 0,
			disk2_type TEXT NOT NULL DEFAULT '',
			disks_json TEXT NOT NULL DEFAULT '',
			traffic_tb REAL NOT NULL DEFAULT 0,
			traffic_set INTEGER NOT NULL DEFAULT 0,
			traffic_direction TEXT NOT NULL DEFAULT '',
			bandwidth_value REAL NOT NULL DEFAULT 0,
			bandwidth_unit TEXT NOT NULL DEFAULT 'Mbps',
			bandwidth_mbps INTEGER NOT NULL DEFAULT 0,
			expires_at TEXT NOT NULL,
			renewal_price REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'CNY',
			notes TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL DEFAULT 0,
			hidden INTEGER NOT NULL DEFAULT 0,
			exclude_cost INTEGER NOT NULL DEFAULT 0,
			billing_months INTEGER NOT NULL DEFAULT 12,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_vps_expires ON vps(expires_at);`,
		`CREATE TABLE IF NOT EXISTS reminder_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_type TEXT NOT NULL,
			item_id INTEGER NOT NULL,
			days_before INTEGER NOT NULL,
			sent_date TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(item_type, item_id, days_before, sent_date)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_reminder_log_date ON reminder_log(sent_date);`,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return err
		}
	}

	alter := []string{
		`ALTER TABLE domains ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE domains ADD COLUMN exclude_cost INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE domains ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE domains ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN region TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE vps ADD COLUMN cores INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN memory_gb REAL NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN disk_gb INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN disk_type TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE vps ADD COLUMN disk2_gb INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN disk2_type TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE vps ADD COLUMN disks_json TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE vps ADD COLUMN traffic_tb REAL NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN traffic_set INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN traffic_direction TEXT NOT NULL DEFAULT '';`,
		`ALTER TABLE vps ADD COLUMN bandwidth_value REAL NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN bandwidth_unit TEXT NOT NULL DEFAULT 'Mbps';`,
		`ALTER TABLE vps ADD COLUMN bandwidth_mbps INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN exclude_cost INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN billing_months INTEGER NOT NULL DEFAULT 12;`,
		`ALTER TABLE vps ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE vps ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;`,
	}
	for _, s := range alter {
		if _, err := db.ExecContext(ctx, s); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				continue
			}
			return err
		}
	}

	// Best-effort backfill for old rows (idempotent).
	if _, err := db.ExecContext(ctx, `UPDATE vps SET traffic_set = 1 WHERE traffic_set = 0 AND traffic_tb > 0;`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE vps SET bandwidth_value = bandwidth_mbps, bandwidth_unit = 'Mbps' WHERE bandwidth_value = 0 AND bandwidth_mbps > 0;`); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE vps SET billing_months = 12 WHERE billing_months <= 0;`); err != nil {
		return err
	}
	if err := backfillSortOrder(ctx, db, "domains", "expires_at ASC, id ASC"); err != nil {
		return err
	}
	if err := backfillSortOrder(ctx, db, "vps", "expires_at ASC, id ASC"); err != nil {
		return err
	}
	return nil
}
func ensureDefaults(db *sql.DB, cfg config) error {
	if _, ok, err := getSetting(db, settingPublicMode); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingPublicMode, "0"); err != nil {
			return err
		}
	}

	defaultPublicSettings := []string{
		settingPublicShowTotals,
		settingPublicShowDomainRegistrar,
		settingPublicShowDomainExpiry,
		settingPublicShowDomainPrice,
		settingPublicShowDomainNotes,
		settingPublicShowVPSIP,
		settingPublicShowVPSRegion,
		settingPublicShowVPSSpecs,
		settingPublicShowVPSExpiry,
		settingPublicShowVPSPrice,
		settingPublicShowVPSNotes,
	}
	for _, key := range defaultPublicSettings {
		if _, ok, err := getSetting(db, key); err != nil {
			return err
		} else if !ok {
			if err := setSetting(db, key, "1"); err != nil {
				return err
			}
		}
	}

	if _, ok, err := getSetting(db, settingFxAutoUpdate); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingFxAutoUpdate, "1"); err != nil {
			return err
		}
	}

	if _, ok, err := getSetting(db, settingFxEURToUSD); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingFxEURToUSD, strconv.FormatFloat(defaultFxEURToUSD, 'f', -1, 64)); err != nil {
			return err
		}
		if err := setSetting(db, settingFxSource, fxSourceManual); err != nil {
			return err
		}
	}
	if _, ok, err := getSetting(db, settingFxEURToCNY); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingFxEURToCNY, strconv.FormatFloat(defaultFxEURToCNY, 'f', -1, 64)); err != nil {
			return err
		}
		if err := setSetting(db, settingFxSource, fxSourceManual); err != nil {
			return err
		}
	}
	if _, ok, err := getSetting(db, settingFxEURToRUB); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingFxEURToRUB, strconv.FormatFloat(defaultFxEURToRUB, 'f', -1, 64)); err != nil {
			return err
		}
		if err := setSetting(db, settingFxSource, fxSourceManual); err != nil {
			return err
		}
	}

	if _, ok, err := getSetting(db, settingCookieSecret); err != nil {
		return err
	} else if !ok {
		secret, err := newCookieSecret()
		if err != nil {
			return err
		}
		if err := setSetting(db, settingCookieSecret, secret); err != nil {
			return err
		}
	}

	if err := ensureAdminPassword(db, cfg.adminPassword); err != nil {
		return err
	}

	if _, ok, err := getSetting(db, settingEmailEnabled); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingEmailEnabled, "0"); err != nil {
			return err
		}
	}
	if _, ok, err := getSetting(db, settingEmailDays); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingEmailDays, defaultEmailDaysList); err != nil {
			return err
		}
	}
	if _, ok, err := getSetting(db, settingEmailEncrypt); err != nil {
		return err
	} else if !ok {
		if err := setSetting(db, settingEmailEncrypt, "auto"); err != nil {
			return err
		}
	}

	return nil
}
func getSetting(db *sql.DB, key string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}
func setSetting(db *sql.DB, key, value string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
func mustGetBoolSetting(db *sql.DB, key string, fallback bool) bool {
	v, ok, err := getSetting(db, key)
	if err != nil || !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
func mustGetFloatSetting(db *sql.DB, key string, fallback float64) float64 {
	v, ok, err := getSetting(db, key)
	if err != nil || !ok {
		return fallback
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || !isFinitePositive(f) {
		return fallback
	}
	return f
}
func mustGetIntSetting(db *sql.DB, key string, fallback int) int {
	v, ok, err := getSetting(db, key)
	if err != nil || !ok {
		return fallback
	}
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || i < 0 {
		return fallback
	}
	return i
}
func mustGetStringSetting(db *sql.DB, key, fallback string) string {
	v, ok, err := getSetting(db, key)
	if err != nil || !ok {
		return fallback
	}
	return v
}
func backfillSortOrder(ctx context.Context, db *sql.DB, table string, orderBy string) error {
	if table == "" || orderBy == "" {
		return nil
	}
	query := fmt.Sprintf("SELECT id, sort_order FROM %s ORDER BY %s", table, orderBy)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	maxSort := 0
	var missing []int64
	for rows.Next() {
		var id int64
		var sortOrder int
		if err := rows.Scan(&id, &sortOrder); err != nil {
			return err
		}
		if sortOrder > maxSort {
			maxSort = sortOrder
		}
		if sortOrder == 0 {
			missing = append(missing, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf("UPDATE %s SET sort_order = ? WHERE id = ?", table))
	if err != nil {
		return err
	}
	defer stmt.Close()

	next := maxSort
	for _, id := range missing {
		next++
		if _, err := stmt.ExecContext(ctx, next, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func nextSortOrder(ctx context.Context, db *sql.DB, table string) (int, error) {
	if table == "" {
		return 0, errors.New("missing table")
	}
	var next int
	query := fmt.Sprintf("SELECT COALESCE(MAX(sort_order), 0) + 1 FROM %s", table)
	if err := db.QueryRowContext(ctx, query).Scan(&next); err != nil {
		return 0, err
	}
	if next <= 0 {
		next = 1
	}
	return next, nil
}
