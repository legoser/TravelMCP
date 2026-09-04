package places

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"travelmcp/internal/model"
)

// Deprecated: Service с *sql.DB нарушает изоляцию репозитория (торчит *sql.DB).
// Сохранён для совместимости со sqlite_deprecated. Новый код используйте store.Store
// (UpsertPlace/UpsertTerminal/GetPlaceCity напрямую).
type Service struct {
	db *sql.DB
}

func New(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) UpsertPlace(ctx context.Context, p *model.Place, names []model.PlaceName) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	id, err := s.upsertPlaceTx(ctx, tx, p, names)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Service) upsertPlaceTx(ctx context.Context, tx *sql.Tx, p *model.Place, names []model.PlaceName) (int64, error) {
	var id int64
	if p.ID != 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO places(id, parent_id, admin_level, level, lat, lon, tz, valid_from, valid_to) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET parent_id=excluded.parent_id, admin_level=excluded.admin_level, level=excluded.level, lat=excluded.lat, lon=excluded.lon, tz=excluded.tz, valid_to=excluded.valid_to`, p.ID, p.ParentID, p.AdminLevel, p.Level, p.Lat, p.Lon, p.Tz, p.ValidFrom.Format("2006-01-02"), nullableDate(p.ValidTo))
		if err != nil {
			return 0, err
		}
		id = p.ID
	} else {
		res, err := tx.ExecContext(ctx, `INSERT INTO places(parent_id, admin_level, level, lat, lon, tz, valid_from, valid_to) VALUES(?,?,?,?,?,?,?,?)`, p.ParentID, p.AdminLevel, p.Level, p.Lat, p.Lon, p.Tz, p.ValidFrom.Format("2006-01-02"), nullableDate(p.ValidTo))
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	}
	for _, n := range names {
		if _, err := tx.ExecContext(ctx, `INSERT INTO place_names(place_id, lang, name, normalized) VALUES(?,?,?,?) ON CONFLICT(place_id, lang) DO UPDATE SET name=excluded.name, normalized=excluded.normalized`, id, n.Lang, n.Name, normalize(n.Name)); err != nil {
			return 0, err
		}
	}
	if err := rebuildClosureSubtreeTx(ctx, tx, id); err != nil {
		return 0, err
	}
	return id, nil
}

func rebuildClosureSubtreeTx(ctx context.Context, tx *sql.Tx, root int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM place_closure WHERE descendant_id IN (SELECT descendant_id FROM place_closure WHERE ancestor_id = ?) OR descendant_id = ?`, root, root); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,0)`, root, root); err != nil {
		return err
	}
	var parent sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT parent_id FROM places WHERE id=?`, root).Scan(&parent); err != nil {
		return err
	}
	if parent.Valid {
		rows, err := tx.QueryContext(ctx, `SELECT ancestor_id, depth FROM place_closure WHERE descendant_id=?`, parent.Int64)
		if err != nil {
			return err
		}
		type ancRow struct {
			anc int64
			d   int
		}
		var ancestors []ancRow
		for rows.Next() {
			var anc int64
			var d int
			if err := rows.Scan(&anc, &d); err != nil {
				rows.Close()
				return err
			}
			ancestors = append(ancestors, ancRow{anc, d})
		}
		rows.Close()
		for _, a := range ancestors {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,?)`, a.anc, root, a.d+1); err != nil {
				return err
			}
		}
		descendants, err := collectDescendants(ctx, tx, root)
		if err != nil {
			return err
		}
		for _, desc := range descendants {
			if desc == root {
				continue
			}
			rows2, err := tx.QueryContext(ctx, `SELECT ancestor_id, depth FROM place_closure WHERE descendant_id=?`, parent.Int64)
			if err != nil {
				return err
			}
			var ancestors2 []ancRow
			for rows2.Next() {
				var anc int64
				var d int
				rows2.Scan(&anc, &d)
				ancestors2 = append(ancestors2, ancRow{anc, d})
			}
			rows2.Close()
			for _, a := range ancestors2 {
				depthDesc := depthOf(ctx, tx, root, desc)
				tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,?)`, a.anc, desc, a.d+1+depthDesc)
			}
		}
	}
	return nil
}

func collectDescendants(ctx context.Context, tx *sql.Tx, root int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE sub(id) AS (SELECT ? UNION ALL SELECT p.id FROM places p JOIN sub ON p.parent_id=sub.id) SELECT id FROM sub`, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, nil
}

func depthOf(ctx context.Context, tx *sql.Tx, ancestor, descendant int64) int {
	var d int
	_ = tx.QueryRowContext(ctx, `WITH RECURSIVE chain(id, depth) AS (SELECT ?,0 UNION ALL SELECT p.id, chain.depth+1 FROM places p JOIN chain ON p.parent_id=chain.id WHERE p.id != ?) SELECT depth FROM chain WHERE id=?`, ancestor, ancestor, descendant).Scan(&d)
	// fallback via place_closure
	_ = tx.QueryRowContext(ctx, `SELECT depth FROM place_closure WHERE ancestor_id=? AND descendant_id=?`, ancestor, descendant).Scan(&d)
	return d
}

func (s *Service) CityForTerminal(ctx context.Context, placeID int64) (int64, string, error) {
	var id int64
	var tz string
	err := s.db.QueryRowContext(ctx, `SELECT p.id, p.tz FROM place_closure pc JOIN places p ON p.id=pc.ancestor_id WHERE pc.descendant_id=? AND p.level=4 LIMIT 1`, placeID).Scan(&id, &tz)
	if err != nil {
		return 0, "", fmt.Errorf("city not found: %w", err)
	}
	return id, tz, nil
}

func (s *Service) RebuildAll(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM place_closure`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, parent_id FROM places ORDER BY id`)
	if err != nil {
		return err
	}
	type row struct {
		id     int64
		parent sql.NullInt64
	}
	var all []row
	for rows.Next() {
		var r row
		rows.Scan(&r.id, &r.parent)
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,0)`, r.id, r.id)
		if r.parent.Valid {
			ancRows, _ := tx.QueryContext(ctx, `SELECT ancestor_id, depth FROM place_closure WHERE descendant_id=?`, r.parent.Int64)
			if ancRows != nil {
				type ancRow struct {
					anc int64
					d   int
				}
				var ancestors []ancRow
				for ancRows.Next() {
					var anc int64
					var d int
					ancRows.Scan(&anc, &d)
					ancestors = append(ancestors, ancRow{anc, d})
				}
				ancRows.Close()
				for _, a := range ancestors {
					tx.ExecContext(ctx, `INSERT OR IGNORE INTO place_closure(ancestor_id, descendant_id, depth) VALUES(?,?,?)`, a.anc, r.id, a.d+1)
				}
			}
		}
	}
	return tx.Commit()
}

func (s *Service) VerifyClosureConsistency(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.parent_id FROM places p`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parent := map[int64]*int64{}
	ids := []int64{}
	for rows.Next() {
		var id int64
		var par sql.NullInt64
		rows.Scan(&id, &par)
		ids = append(ids, id)
		if par.Valid {
			v := par.Int64
			parent[id] = &v
		} else {
			parent[id] = nil
		}
	}
	var issues []string
	for _, id := range ids {
		expected := ancestorsFromParent(parent, id)
		actual := ancestorsFromClosure(ctx, s.db, id)
		if !equalSets(expected, actual) {
			issues = append(issues, fmt.Sprintf("place %d: expected %v actual %v", id, expected, actual))
		}
	}
	return issues, nil
}

func ancestorsFromParent(parent map[int64]*int64, id int64) map[int64]bool {
	m := map[int64]bool{id: true}
	cur := id
	for {
		p := parent[cur]
		if p == nil {
			break
		}
		m[*p] = true
		cur = *p
	}
	return m
}

func ancestorsFromClosure(ctx context.Context, db *sql.DB, id int64) map[int64]bool {
	rows, _ := db.QueryContext(ctx, `SELECT ancestor_id FROM place_closure WHERE descendant_id=?`, id)
	m := map[int64]bool{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var a int64
			rows.Scan(&a)
			m[a] = true
		}
	}
	return m
}

func equalSets(a, b map[int64]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func nullableDate(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), " ")
}
