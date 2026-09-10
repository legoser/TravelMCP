package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

func (p *PostgresStore) ListIdentifierSchemes(ctx context.Context) ([]IdentifierSchemeRow, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT code, system, display_name, priority, is_resolvable, is_merge_key FROM identifier_schemes ORDER BY system, code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IdentifierSchemeRow{}
	for rows.Next() {
		var s IdentifierSchemeRow
		if err := rows.Scan(&s.Code, &s.System, &s.DisplayName, &s.Priority, &s.IsResolvable, &s.IsMergeKey); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *PostgresStore) UpsertIdentifierScheme(ctx context.Context, s IdentifierSchemeRow) error {
	if p.pool == nil {
		return errNotImplemented
	}
	if s.Code == "" || s.System == "" || s.DisplayName == "" {
		return errors.New("store: identifier scheme: code, system и display_name обязательны")
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO identifier_schemes(code, system, display_name, priority, is_resolvable, is_merge_key) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(code) DO UPDATE SET system=EXCLUDED.system, display_name=EXCLUDED.display_name, priority=EXCLUDED.priority, is_resolvable=EXCLUDED.is_resolvable, is_merge_key=EXCLUDED.is_merge_key`,
		s.Code, s.System, s.DisplayName, s.Priority, s.IsResolvable, s.IsMergeKey)
	return err
}

func (p *PostgresStore) DeleteIdentifierScheme(ctx context.Context, code string) error {
	if p.pool == nil {
		return errNotImplemented
	}
	ct, err := p.pool.Exec(ctx, `DELETE FROM identifier_schemes WHERE code=$1`, code)
	if err != nil {
		if isForeignKeyViolation(err) {
			return errors.New("тип кода используется у существующих идентификаторов — сначала убрать коды")
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("тип кода не найден")
	}
	return nil
}

func (p *PostgresStore) ListIdentifierSystems(ctx context.Context) ([]string, error) {
	if p.pool == nil {
		return nil, errNotImplemented
	}
	rows, err := p.pool.Query(ctx, `SELECT DISTINCT system FROM identifier_schemes ORDER BY system`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
