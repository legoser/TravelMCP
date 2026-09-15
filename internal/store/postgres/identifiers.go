package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Primary-идентификатор терминала выбирается по рангу системы из
// identifier_schemes.priority (данные, не хардкод в Go): новый источник
// добавляется записью в справочник, без правки кода.

type identDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// schemePriority — приоритет системы; для систем без записи в
// identifier_schemes приоритет 0 (минимальный).
func schemePriority(ctx context.Context, db identDB, system string) (int, error) {
	var pr int
	if err := db.QueryRow(ctx, `SELECT coalesce(max(priority),0) FROM identifier_schemes WHERE system=$1`, system).Scan(&pr); err != nil {
		return 0, err
	}
	return pr, nil
}

func applyIdentifierPrimaryRule(ctx context.Context, db identDB, terminalID int64, system, codeType, code string) error {
	var cur string
	err := db.QueryRow(ctx, `SELECT system FROM terminal_identifiers WHERE terminal_id=$1 AND is_primary ORDER BY code_type, code LIMIT 1`, terminalID).Scan(&cur)
	if err != nil {
		_, err2 := db.Exec(ctx, `UPDATE terminal_identifiers SET is_primary=true WHERE terminal_id=$1 AND system=$2 AND code_type=$3 AND code=$4`, terminalID, system, codeType, code)
		return err2
	}
	newPr, err := schemePriority(ctx, db, system)
	if err != nil {
		return err
	}
	curPr, err := schemePriority(ctx, db, cur)
	if err != nil {
		return err
	}
	if newPr <= curPr {
		return nil
	}
	if _, err := db.Exec(ctx, `UPDATE terminal_identifiers SET is_primary=false WHERE terminal_id=$1 AND is_primary`, terminalID); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `UPDATE terminal_identifiers SET is_primary=true WHERE terminal_id=$1 AND system=$2 AND code_type=$3 AND code=$4`, terminalID, system, codeType, code)
	return err
}
