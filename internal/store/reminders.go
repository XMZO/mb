package store

import (
	"context"
	"database/sql"

	"mb/internal/asset"
)

// ReminderRecord 表示"某项资产在 SentDate 这天发过提前 DaysBefore 天的提醒"。
type ReminderRecord struct {
	Kind       asset.Kind
	ID         int64
	DaysBefore int
	SentDate   string // YYYY-MM-DD
}

// ReminderLog 返回 since(含)之后的提醒记录。
func (s *Store) ReminderLog(ctx context.Context, since string) ([]ReminderRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_type, item_id, days_before, sent_date FROM reminder_log WHERE sent_date >= ?`, since)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r rowScanner) (ReminderRecord, error) {
		var rec ReminderRecord
		err := r.Scan(&rec.Kind, &rec.ID, &rec.DaysBefore, &rec.SentDate)
		return rec, err
	})
}

// AddReminderLog 记录已发送的提醒;重复的记录会被忽略。
func (s *Store) AddReminderLog(ctx context.Context, records []ReminderRecord) error {
	if len(records) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reminder_log
			(item_type, item_id, days_before, sent_date, created_at) VALUES(?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := timestamp()
		for _, rec := range records {
			if _, err := stmt.ExecContext(ctx, string(rec.Kind), rec.ID, rec.DaysBefore, rec.SentDate, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneReminderLog 删除 before 之前的提醒记录,避免日志表无限增长。
func (s *Store) PruneReminderLog(ctx context.Context, before string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM reminder_log WHERE sent_date < ?`, before)
	return err
}
