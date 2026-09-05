package postgres

import (
	"context"
	"log/slog"

	"travelmcp/internal/pricing"
)

func (p *PostgresStore) UpsertZone(ctx context.Context, z ZoneRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO zones(zone_id, name_ru, name_en) VALUES($1,$2,$3) ON CONFLICT(zone_id) DO UPDATE SET name_ru=EXCLUDED.name_ru, name_en=EXCLUDED.name_en`, z.ZoneID, z.NameRu, z.NameEn)
	return err
}

func (p *PostgresStore) UpsertFareAttribute(ctx context.Context, f FareAttributeRow) error {
	if p.pool == nil {
		return nil
	}
	cur := pricing.CurrencyOrDefault(f.Currency)
	if err := pricing.ValidateCurrency(cur); err != nil {
		slog.Warn("store/postgres: invalid currency, using default", "fare", f.FareID, "currency", f.Currency, "err", err)
		cur = string(pricing.DefaultCurrency())
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO fare_attributes(fare_id, price, currency, basis) VALUES($1,$2,$3,$4) ON CONFLICT(fare_id) DO UPDATE SET price=EXCLUDED.price, currency=EXCLUDED.currency, basis=EXCLUDED.basis`, f.FareID, f.Price, cur, f.Basis)
	return err
}

func (p *PostgresStore) UpsertFareRule(ctx context.Context, r FareRuleRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO fare_rules(fare_id, route_id, origin_zone, destination_zone) VALUES($1,$2,$3,$4) ON CONFLICT(fare_id, route_id, origin_zone, destination_zone) DO NOTHING`, r.FareID, r.RouteID, r.OriginZone, r.DestinationZone)
	return err
}

func (p *PostgresStore) UpsertStopZone(ctx context.Context, s StopZoneRow) error {
	if p.pool == nil {
		return nil
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO stop_zones(stop_id, zone_id) VALUES($1,$2) ON CONFLICT(stop_id, zone_id) DO NOTHING`, s.StopID, s.ZoneID)
	return err
}

func (p *PostgresStore) ListZones(ctx context.Context) ([]ZoneRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT zone_id, name_ru, name_en FROM zones`)
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

func (p *PostgresStore) ListFareAttributes(ctx context.Context) ([]FareAttributeRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT fare_id, price, currency, basis FROM fare_attributes`)
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

func (p *PostgresStore) ListFareRules(ctx context.Context) ([]FareRuleRow, error) {
	if p.pool == nil {
		return nil, nil
	}
	rows, err := p.pool.Query(ctx, `SELECT fare_id, route_id, origin_zone, destination_zone FROM fare_rules`)
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

func (t *pgTxStore) UpsertZone(ctx context.Context, z ZoneRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO zones(zone_id, name_ru, name_en) VALUES($1,$2,$3) ON CONFLICT(zone_id) DO UPDATE SET name_ru=EXCLUDED.name_ru, name_en=EXCLUDED.name_en`, z.ZoneID, z.NameRu, z.NameEn)
	return err
}

func (t *pgTxStore) UpsertFareAttribute(ctx context.Context, f FareAttributeRow) error {
	cur := pricing.CurrencyOrDefault(f.Currency)
	if err := pricing.ValidateCurrency(cur); err != nil {
		slog.Warn("store/postgres: invalid currency, using default", "fare", f.FareID, "currency", f.Currency, "err", err)
		cur = string(pricing.DefaultCurrency())
	}
	_, err := t.tx.Exec(ctx, `INSERT INTO fare_attributes(fare_id, price, currency, basis) VALUES($1,$2,$3,$4) ON CONFLICT(fare_id) DO UPDATE SET price=EXCLUDED.price, currency=EXCLUDED.currency, basis=EXCLUDED.basis`, f.FareID, f.Price, cur, f.Basis)
	return err
}

func (t *pgTxStore) UpsertFareRule(ctx context.Context, r FareRuleRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO fare_rules(fare_id, route_id, origin_zone, destination_zone) VALUES($1,$2,$3,$4) ON CONFLICT(fare_id, route_id, origin_zone, destination_zone) DO NOTHING`, r.FareID, r.RouteID, r.OriginZone, r.DestinationZone)
	return err
}

func (t *pgTxStore) UpsertStopZone(ctx context.Context, s StopZoneRow) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO stop_zones(stop_id, zone_id) VALUES($1,$2) ON CONFLICT(stop_id, zone_id) DO NOTHING`, s.StopID, s.ZoneID)
	return err
}

func (t *pgTxStore) ListZones(ctx context.Context) ([]ZoneRow, error) { return t.parent.ListZones(ctx) }
func (t *pgTxStore) ListFareAttributes(ctx context.Context) ([]FareAttributeRow, error) {
	return t.parent.ListFareAttributes(ctx)
}
func (t *pgTxStore) ListFareRules(ctx context.Context) ([]FareRuleRow, error) {
	return t.parent.ListFareRules(ctx)
}
