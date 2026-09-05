package sqlite

import (
	"context"
	"log/slog"

	"travelmcp/internal/pricing"
)

func (s *SQLiteStore) UpsertZone(ctx context.Context, z ZoneRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO zones(zone_id, name_ru, name_en) VALUES(?,?,?) ON CONFLICT(zone_id) DO UPDATE SET name_ru=excluded.name_ru, name_en=excluded.name_en`, z.ZoneID, z.NameRu, z.NameEn)
	return err
}

func (s *SQLiteStore) UpsertFareAttribute(ctx context.Context, f FareAttributeRow) error {
	currency := pricing.CurrencyOrDefault(f.Currency)
	if err := pricing.ValidateCurrency(currency); err != nil {
		slog.Warn("store/sqlite: invalid currency, using default", "fare", f.FareID, "currency", f.Currency, "err", err)
		currency = string(pricing.DefaultCurrency())
	}
	basis := f.Basis
	if basis == "" {
		basis = "fare"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO fare_attributes(fare_id, price, currency, basis) VALUES(?,?,?,?) ON CONFLICT(fare_id) DO UPDATE SET price=excluded.price, currency=excluded.currency`, f.FareID, f.Price, currency, basis)
	return err
}

func (s *SQLiteStore) UpsertFareRule(ctx context.Context, r FareRuleRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO fare_rules(fare_id, route_id, origin_zone, destination_zone) VALUES(?,?,?,?) ON CONFLICT(fare_id, route_id, origin_zone, destination_zone) DO NOTHING`, r.FareID, r.RouteID, r.OriginZone, r.DestinationZone)
	return err
}

func (s *SQLiteStore) UpsertStopZone(ctx context.Context, sz StopZoneRow) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stop_zones(stop_id, zone_id) VALUES(?,?) ON CONFLICT(stop_id, zone_id) DO NOTHING`, sz.StopID, sz.ZoneID)
	return err
}

func (s *SQLiteStore) ListZones(ctx context.Context) ([]ZoneRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT zone_id, name_ru, name_en FROM zones`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ZoneRow
	for rows.Next() {
		var z ZoneRow
		if err := rows.Scan(&z.ZoneID, &z.NameRu, &z.NameEn); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, nil
}

func (s *SQLiteStore) ListFareAttributes(ctx context.Context) ([]FareAttributeRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fare_id, price, currency, basis FROM fare_attributes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FareAttributeRow
	for rows.Next() {
		var f FareAttributeRow
		if err := rows.Scan(&f.FareID, &f.Price, &f.Currency, &f.Basis); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *SQLiteStore) ListFareRules(ctx context.Context) ([]FareRuleRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fare_id, route_id, origin_zone, destination_zone FROM fare_rules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FareRuleRow
	for rows.Next() {
		var r FareRuleRow
		if err := rows.Scan(&r.FareID, &r.RouteID, &r.OriginZone, &r.DestinationZone); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (t *txStore) UpsertZone(ctx context.Context, z ZoneRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO zones(zone_id, name_ru, name_en) VALUES(?,?,?) ON CONFLICT(zone_id) DO UPDATE SET name_ru=excluded.name_ru`, z.ZoneID, z.NameRu, z.NameEn)
	return err
}

func (t *txStore) UpsertFareAttribute(ctx context.Context, f FareAttributeRow) error {
	currency := pricing.CurrencyOrDefault(f.Currency)
	if err := pricing.ValidateCurrency(currency); err != nil {
		slog.Warn("store/sqlite: invalid currency, using default", "fare", f.FareID, "currency", f.Currency, "err", err)
		currency = string(pricing.DefaultCurrency())
	}
	basis := f.Basis
	if basis == "" {
		basis = "fare"
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO fare_attributes(fare_id, price, currency, basis) VALUES(?,?,?,?) ON CONFLICT(fare_id) DO UPDATE SET price=excluded.price`, f.FareID, f.Price, currency, basis)
	return err
}

func (t *txStore) UpsertFareRule(ctx context.Context, r FareRuleRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO fare_rules(fare_id, route_id, origin_zone, destination_zone) VALUES(?,?,?,?) ON CONFLICT(fare_id, route_id, origin_zone, destination_zone) DO NOTHING`, r.FareID, r.RouteID, r.OriginZone, r.DestinationZone)
	return err
}

func (t *txStore) UpsertStopZone(ctx context.Context, sz StopZoneRow) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO stop_zones(stop_id, zone_id) VALUES(?,?) ON CONFLICT(stop_id, zone_id) DO NOTHING`, sz.StopID, sz.ZoneID)
	return err
}

func (t *txStore) ListZones(ctx context.Context) ([]ZoneRow, error) { return t.parent.ListZones(ctx) }
func (t *txStore) ListFareAttributes(ctx context.Context) ([]FareAttributeRow, error) {
	return t.parent.ListFareAttributes(ctx)
}
func (t *txStore) ListFareRules(ctx context.Context) ([]FareRuleRow, error) {
	return t.parent.ListFareRules(ctx)
}
