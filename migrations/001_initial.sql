-- PostGIS + pg_trgm + unaccent

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 3.1 справочники
CREATE TABLE IF NOT EXISTS regions (
  code text PRIMARY KEY,
  name_ru text NOT NULL,
  name_en text,
  geom geometry(MultiPolygon, 4326),
  density_class text CHECK (density_class IN ('rural','suburban','urban','metro')) DEFAULT 'rural' NOT NULL
);

CREATE TABLE IF NOT EXISTS transport_modes (
  mode text PRIMARY KEY,
  max_speed real NOT NULL
);
INSERT INTO transport_modes(mode, max_speed) VALUES
  ('bus', 120), ('coach', 120), ('tram', 120), ('rail', 250), ('flight', 1000)
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS providers (
  code text PRIMARY KEY,
  name text NOT NULL
);
INSERT INTO providers(code, name) VALUES
  ('motis','MOTIS/OSM'), ('mintrans','Минтранс'), ('yandex','Яндекс'), ('osm','OSM'), ('gtfs','GTFS'), ('nominatim','Nominatim')
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS carriers (
  id bigserial PRIMARY KEY,
  inn text,
  name_ru text NOT NULL,
  name_en text,
  address text,
  iata text,
  icao text,
  sirena text
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_carriers_inn ON carriers(inn) WHERE inn IS NOT NULL;
-- uniq_carriers_provider_code removed: carrier_identifiers is source of truth
INSERT INTO carriers(id, inn, name_ru) VALUES (0, NULL, 'Неизвестный перевозчик') ON CONFLICT DO NOTHING;
SELECT setval('carriers_id_seq', (SELECT GREATEST(MAX(id),0)+1 FROM carriers), false);

CREATE TABLE IF NOT EXISTS carrier_identifiers (
  carrier_id bigint NOT NULL REFERENCES carriers(id) ON DELETE CASCADE,
  system text NOT NULL,
  code_type text NOT NULL,
  code text NOT NULL,
  PRIMARY KEY (carrier_id, system, code_type),
  UNIQUE (system, code)
);

-- 3.2 иерархия мест
CREATE TABLE IF NOT EXISTS places (
  id bigserial PRIMARY KEY,
  parent_id bigint REFERENCES places(id) DEFERRABLE INITIALLY DEFERRED,
  admin_level int NOT NULL,
  level smallint NOT NULL,
  geom geography(Point,4326),
  bbox geometry(Polygon,4326),
  tz text,
  valid_from date NOT NULL DEFAULT CURRENT_DATE,
  valid_to date,
  is_current bool GENERATED ALWAYS AS (valid_to IS NULL) STORED
);
CREATE INDEX IF NOT EXISTS idx_places_parent ON places(parent_id);
CREATE INDEX IF NOT EXISTS idx_places_level ON places(level);
CREATE INDEX IF NOT EXISTS idx_places_is_current ON places(is_current) WHERE is_current;

CREATE TABLE IF NOT EXISTS place_closure (
  ancestor_id bigint NOT NULL REFERENCES places(id) ON DELETE CASCADE,
  descendant_id bigint NOT NULL REFERENCES places(id) ON DELETE CASCADE,
  depth smallint NOT NULL,
  PRIMARY KEY (ancestor_id, descendant_id)
);
CREATE INDEX IF NOT EXISTS idx_place_closure_descendant ON place_closure(descendant_id);
CREATE INDEX IF NOT EXISTS idx_place_closure_descendant_depth ON place_closure(descendant_id, depth);

CREATE TABLE IF NOT EXISTS place_names (
  place_id bigint NOT NULL REFERENCES places(id) ON DELETE CASCADE,
  lang text NOT NULL CHECK (lang IN ('ru','en')),
  name text NOT NULL,
  normalized text NOT NULL,
  PRIMARY KEY (place_id, lang)
);
CREATE INDEX IF NOT EXISTS idx_place_names_normalized ON place_names USING gin (normalized gin_trgm_ops);

-- 3.3 терминалы и остановки (канон)
CREATE TABLE IF NOT EXISTS terminals (
  id bigserial PRIMARY KEY,
  place_id bigint REFERENCES places(id) ON DELETE SET NULL,
  geom geography(Point,4326) NOT NULL,
  tz text,
  validity daterange,
  osm_compatible_name text,
  valid_from date NOT NULL DEFAULT CURRENT_DATE,
  valid_to date,
  is_current bool GENERATED ALWAYS AS (valid_to IS NULL) STORED,
  last_verified_at timestamptz,
  is_locked bool NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS idx_terminals_place ON terminals(place_id);
CREATE INDEX IF NOT EXISTS idx_terminals_geom ON terminals USING gist(geom);
CREATE INDEX IF NOT EXISTS idx_terminals_validity ON terminals USING gist(validity);
CREATE INDEX IF NOT EXISTS idx_terminals_osm_name ON terminals USING gin(osm_compatible_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_terminals_is_current ON terminals(is_current) WHERE is_current;

CREATE TABLE IF NOT EXISTS stops_canonical (
  id bigserial PRIMARY KEY,
  terminal_id bigint NOT NULL REFERENCES terminals(id) ON DELETE CASCADE,
  geom geography(Point,4326),
  stop_type text,
  validity daterange
);
CREATE INDEX IF NOT EXISTS idx_stops_canonical_terminal ON stops_canonical(terminal_id);

CREATE TABLE IF NOT EXISTS terminal_names (
  terminal_id bigint NOT NULL REFERENCES terminals(id) ON DELETE CASCADE,
  lang text NOT NULL CHECK (lang IN ('ru','en')),
  name text NOT NULL,
  is_primary bool NOT NULL DEFAULT true,
  PRIMARY KEY (terminal_id, lang)
);

CREATE TABLE IF NOT EXISTS stop_names (
  stop_id bigint NOT NULL REFERENCES stops_canonical(id) ON DELETE CASCADE,
  lang text NOT NULL CHECK (lang IN ('ru','en')),
  name text NOT NULL,
  PRIMARY KEY (stop_id, lang)
);

CREATE TABLE IF NOT EXISTS terminal_identifiers (
  terminal_id bigint NOT NULL REFERENCES terminals(id) ON DELETE CASCADE,
  system text NOT NULL CHECK (system IN ('mintrans','yandex','osm','gtfs','motis','nominatim')),
  code_type text NOT NULL CHECK (code_type IN ('op_reg','station_code','osm_id','gtfs_stop_id','motis_id','motis_stop_id','area','yandex_code')),
  code text NOT NULL,
  is_primary bool NOT NULL DEFAULT false,
  PRIMARY KEY (terminal_id, system, code_type),
  UNIQUE (system, code)
);

CREATE TABLE IF NOT EXISTS terminal_tags (
  terminal_id bigint NOT NULL REFERENCES terminals(id) ON DELETE CASCADE PRIMARY KEY,
  tags jsonb NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_terminal_tags_gin ON terminal_tags USING gin(tags);

CREATE TABLE IF NOT EXISTS provenance (
  entity_type text NOT NULL CHECK (entity_type IN ('place','terminal','stop','route','trip')),
  entity_id bigint NOT NULL,
  source text NOT NULL REFERENCES providers(code),
  confidence real NOT NULL CHECK (confidence >=0 AND confidence <=1),
  observed_at timestamptz NOT NULL DEFAULT now(),
  raw jsonb,
  actor_id bigint REFERENCES users(id) ON DELETE SET NULL,
  PRIMARY KEY (entity_type, entity_id, source)
);
CREATE TABLE IF NOT EXISTS provenance_history (
  id bigserial PRIMARY KEY,
  entity_type text NOT NULL CHECK (entity_type IN ('place','terminal','stop','route','trip')),
  entity_id bigint NOT NULL,
  source text NOT NULL REFERENCES providers(code),
  confidence real NOT NULL CHECK (confidence >=0 AND confidence <=1),
  observed_at timestamptz NOT NULL DEFAULT now(),
  raw jsonb,
  actor_id bigint REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_prov_hist_entity ON provenance_history(entity_type, entity_id, observed_at DESC);
CREATE OR REPLACE FUNCTION trg_provenance_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO provenance_history(entity_type, entity_id, source, confidence, observed_at, raw, actor_id) VALUES (NEW.entity_type, NEW.entity_id, NEW.source, NEW.confidence, NEW.observed_at, NEW.raw, NEW.actor_id); RETURN NEW; END; $$;
DROP TRIGGER IF EXISTS trg_prov_history ON provenance;
CREATE TRIGGER trg_prov_history AFTER INSERT OR UPDATE ON provenance FOR EACH ROW EXECUTE FUNCTION trg_provenance_history();

CREATE TABLE IF NOT EXISTS review_queue (
  entity_type text NOT NULL CHECK (entity_type IN ('place','terminal','stop','route','trip')),
  entity_id bigint NOT NULL,
  reason text NOT NULL CHECK (reason IN ('low_confidence','missing_coords','duplicate_ambiguous','speed_implausible','seasonal_conflict','carrier_inn_null','conflicts_with_confirmed','legacy_missing_coords')),
  score real,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (entity_type, entity_id, reason)
);

-- legacy таблицы cities/stations/stops/station_codes удалены: импорт теперь пишет сразу в канон terminals/stops_canonical (см. docs/14-plan.md §3.3)

-- 3.4 расписания
CREATE TABLE IF NOT EXISTS routes (
  id bigserial PRIMARY KEY,
  carrier_id bigint REFERENCES carriers(id) ON DELETE SET NULL,
  external_code text NOT NULL,
  short_name text,
  long_name text,
  mode text REFERENCES transport_modes(mode),
  external_uid text,
  ord int,
  source_provider text REFERENCES providers(code),
  valid_from date NOT NULL DEFAULT CURRENT_DATE,
  valid_to date,
  last_verified_at timestamptz,
  UNIQUE(source_provider, external_code)
);
CREATE INDEX IF NOT EXISTS idx_routes_external ON routes(external_code);
CREATE INDEX IF NOT EXISTS idx_routes_carrier ON routes(carrier_id);

CREATE TABLE IF NOT EXISTS services (
  id int PRIMARY KEY,
  provider_id text NOT NULL REFERENCES providers(code),
  name text,
  start_date date,
  end_date date
);
CREATE INDEX IF NOT EXISTS idx_services_provider ON services(provider_id);

CREATE TABLE IF NOT EXISTS service_days (
  service_id int NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  weekday int CHECK(weekday >=0 AND weekday <=6),
  PRIMARY KEY(service_id, weekday)
);

CREATE TABLE IF NOT EXISTS service_exceptions (
  service_id int NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  date date,
  exception_type text CHECK(exception_type IN ('added','removed')),
  PRIMARY KEY(service_id, date)
);

CREATE TABLE IF NOT EXISTS trips (
  id bigserial PRIMARY KEY,
  route_id bigint NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  provider_id text NOT NULL,
  direction text,
  service_days text,
  frequency_flag int,
  period text,
  service_id int REFERENCES services(id) ON DELETE SET NULL,
  headsign_ru text,
  headsign_en text
);
CREATE INDEX IF NOT EXISTS idx_trips_route ON trips(route_id);
CREATE INDEX IF NOT EXISTS idx_trips_service ON trips(service_id);

CREATE TABLE IF NOT EXISTS stop_times (
  trip_id bigint NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
  stop_id bigint NOT NULL REFERENCES stops_canonical(id) ON DELETE CASCADE,
  seq int,
  arrival int,
  departure int,
  pickup_type smallint DEFAULT 0 CHECK(pickup_type IN (0,1,2,3)),
  drop_off_type smallint DEFAULT 0 CHECK(drop_off_type IN (0,1,2,3)),
  dwell int,
  PRIMARY KEY(trip_id, stop_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_stop_times_trip ON stop_times(trip_id, seq);
CREATE INDEX IF NOT EXISTS idx_stop_times_stop_departure ON stop_times(stop_id, departure);

CREATE TABLE IF NOT EXISTS transfers (
  from_stop_id bigint NOT NULL REFERENCES stops_canonical(id) ON DELETE CASCADE,
  to_stop_id bigint NOT NULL REFERENCES stops_canonical(id) ON DELETE CASCADE,
  minutes int,
  min_transfer_time int,
  distance_m int,
  within_station int, -- TODO: boolean, kept int for compat with legacy UpsertTransfer (0/1)
  type text,
  PRIMARY KEY(from_stop_id, to_stop_id)
);
CREATE INDEX IF NOT EXISTS idx_transfers_from ON transfers(from_stop_id);
CREATE INDEX IF NOT EXISTS idx_transfers_to ON transfers(to_stop_id);

CREATE TABLE IF NOT EXISTS frequencies (
  trip_id bigint NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
  start_time int,
  end_time int,
  headway_secs int,
  exact_times int DEFAULT 0,
  PRIMARY KEY(trip_id, start_time)
);

-- качество, импорты, пользователи
CREATE TABLE IF NOT EXISTS quality_issues (
  id bigserial PRIMARY KEY,
  provider_id text,
  entity text,
  entity_id text,
  level text,
  code text,
  msg text,
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_quality_provider_code ON quality_issues(provider_id, code);

CREATE TABLE IF NOT EXISTS imports (
  id bigserial PRIMARY KEY,
  provider_id text NOT NULL REFERENCES providers(code),
  at timestamptz NOT NULL DEFAULT now(),
  records int NOT NULL DEFAULT 0,
  status text NOT NULL DEFAULT 'ok',
  snapshot text,
  checksum text NOT NULL,
  issues int NOT NULL DEFAULT 0,
  UNIQUE(provider_id, checksum)
);
CREATE INDEX IF NOT EXISTS idx_imports_provider_at ON imports(provider_id, at DESC);

CREATE TABLE IF NOT EXISTS users (
  id bigserial PRIMARY KEY,
  email text UNIQUE,
  pass_hash text,
  status text,
  role text,
  created_at timestamptz NOT NULL DEFAULT now(),
  config text
);

CREATE TABLE IF NOT EXISTS api_keys (
  id bigserial PRIMARY KEY,
  user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key text UNIQUE,
  scopes text,
  created_at timestamptz NOT NULL DEFAULT now(),
  last_used timestamptz
);
CREATE INDEX IF NOT EXISTS idx_api_keys_user ON api_keys(user_id);

-- триггеры §3.2 closure
CREATE OR REPLACE FUNCTION fn_rebuild_closure_subtree(p_root bigint) RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$
DECLARE
  r record;
BEGIN
  DELETE FROM place_closure WHERE descendant_id IN (SELECT descendant_id FROM place_closure WHERE ancestor_id = p_root);
  WITH RECURSIVE subtree(id, depth) AS (
    SELECT p_root, 0
    UNION ALL
    SELECT p.id, s.depth+1 FROM places p JOIN subtree s ON p.parent_id = s.id
  ), ancestors(ancestor_id, descendant_id, depth) AS (
    SELECT anc.ancestor_id, sub.id, anc.depth + sub.depth + 1
    FROM subtree sub
    JOIN place_closure anc ON anc.descendant_id = (SELECT parent_id FROM places WHERE id = sub.id)
    WHERE (SELECT parent_id FROM places WHERE id = sub.id) IS NOT NULL
    UNION ALL
    SELECT sub.id, sub.id, 0 FROM subtree sub
  )
  INSERT INTO place_closure(ancestor_id, descendant_id, depth)
  SELECT ancestor_id, descendant_id, depth FROM ancestors
  ON CONFLICT DO NOTHING;
  IF NOT EXISTS (SELECT 1 FROM place_closure WHERE descendant_id = p_root) THEN
    WITH RECURSIVE chain(id, ancestor_id, depth) AS (
      SELECT p_root, p_root, 0
      UNION ALL
      SELECT c.id, p.parent_id, c.depth+1 FROM chain c JOIN places p ON p.id = c.ancestor_id WHERE p.parent_id IS NOT NULL
    )
    INSERT INTO place_closure(ancestor_id, descendant_id, depth)
    SELECT ancestor_id, p_root, depth FROM chain WHERE ancestor_id IS NOT NULL
    ON CONFLICT DO NOTHING;
  END IF;
END; $$;

CREATE OR REPLACE FUNCTION fn_rebuild_closure_all() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM place_closure;
  WITH RECURSIVE tree(id, ancestor_id, depth) AS (
    SELECT id, id, 0 FROM places WHERE parent_id IS NULL
    UNION ALL
    SELECT p.id, tree.ancestor_id, tree.depth+1 FROM places p JOIN tree ON p.parent_id = tree.id
  )
  INSERT INTO place_closure(ancestor_id, descendant_id, depth)
  SELECT ancestor_id, id, depth FROM tree;
END; $$;

CREATE OR REPLACE FUNCTION fn_sync_closure_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    DELETE FROM place_closure WHERE descendant_id = OLD.id OR ancestor_id = OLD.id;
    RETURN OLD;
  ELSIF TG_OP = 'INSERT' THEN
    PERFORM fn_rebuild_closure_subtree(NEW.id);
    RETURN NEW;
  ELSIF TG_OP = 'UPDATE' THEN
    IF OLD.parent_id IS DISTINCT FROM NEW.parent_id THEN
      PERFORM fn_rebuild_closure_subtree(NEW.id);
    END IF;
    RETURN NEW;
  END IF;
  RETURN NULL;
END; $$;

DROP TRIGGER IF EXISTS trg_places_closure_sync ON places;
CREATE TRIGGER trg_places_closure_sync
AFTER INSERT OR UPDATE OF parent_id OR DELETE ON places
FOR EACH ROW EXECUTE FUNCTION fn_sync_closure_trigger();

CREATE OR REPLACE FUNCTION fn_sync_osm_name() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.lang = 'ru' THEN
      UPDATE terminals SET osm_compatible_name = (
        SELECT lower(public.unaccent(name)) FROM terminal_names
        WHERE terminal_id = OLD.terminal_id AND lang='ru' LIMIT 1
      ) WHERE id = OLD.terminal_id;
    END IF;
    RETURN OLD;
  ELSE
    IF NEW.lang <> 'ru' THEN RETURN NEW; END IF;
    UPDATE terminals SET osm_compatible_name = lower(public.unaccent(NEW.name))
    WHERE id = NEW.terminal_id;
    RETURN NEW;
  END IF;
END; $$;
DROP TRIGGER IF EXISTS trg_sync_osm_name ON terminal_names;
CREATE TRIGGER trg_sync_osm_name
AFTER INSERT OR UPDATE OF name OR DELETE ON terminal_names
FOR EACH ROW EXECUTE FUNCTION fn_sync_osm_name();

CREATE OR REPLACE FUNCTION verify_closure_consistency() RETURNS TABLE(descendant_id bigint, expected_ancestors bigint[], actual_ancestors bigint[]) LANGUAGE sql AS $$
  WITH RECURSIVE expected(descendant_id, ancestor_id, depth) AS (
    SELECT id, id, 0 FROM places
    UNION ALL
    SELECT e.descendant_id, p.parent_id, e.depth+1 FROM expected e JOIN places p ON p.id = e.ancestor_id WHERE p.parent_id IS NOT NULL
  )
  SELECT e.descendant_id, array_agg(e.ancestor_id ORDER BY e.depth), array_agg(pc.ancestor_id ORDER BY pc.depth)
  FROM (SELECT descendant_id, ancestor_id, depth FROM expected) e
  FULL JOIN place_closure pc ON pc.descendant_id = e.descendant_id AND pc.ancestor_id = e.ancestor_id
  GROUP BY e.descendant_id HAVING count(*) FILTER (WHERE pc.ancestor_id IS NULL) >0 OR count(*) FILTER (WHERE e.ancestor_id IS NULL)>0;
$$;

CREATE OR REPLACE FUNCTION trg_provenance_no_orphan() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.entity_type='place' AND NOT EXISTS (SELECT 1 FROM places WHERE id=NEW.entity_id) THEN RAISE EXCEPTION 'provenance orphan place %', NEW.entity_id; END IF;
  IF NEW.entity_type='terminal' AND NOT EXISTS (SELECT 1 FROM terminals WHERE id=NEW.entity_id) THEN RAISE EXCEPTION 'provenance orphan terminal %', NEW.entity_id; END IF;
  IF NEW.entity_type='stop' AND NOT EXISTS (SELECT 1 FROM stops_canonical WHERE id=NEW.entity_id) THEN RAISE EXCEPTION 'provenance orphan stop %', NEW.entity_id; END IF;
  RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS trg_provenance_no_orphan ON provenance;
CREATE TRIGGER trg_provenance_no_orphan BEFORE INSERT OR UPDATE ON provenance FOR EACH ROW EXECUTE FUNCTION trg_provenance_no_orphan();

-- 3.5 цены и зоны (Фаза 3)
CREATE TABLE IF NOT EXISTS zones (
  zone_id text PRIMARY KEY,
  name_ru text NOT NULL,
  name_en text,
  geom geometry(MultiPolygon, 4326)
);
CREATE INDEX IF NOT EXISTS idx_zones_geom ON zones USING gist(geom);

CREATE TABLE IF NOT EXISTS fare_attributes (
  fare_id text PRIMARY KEY,
  price numeric NOT NULL CHECK (price >= 0),
  currency text NOT NULL DEFAULT 'RUB',
  payment_method smallint NOT NULL DEFAULT 0 CHECK (payment_method IN (0,1)),
  transfers smallint,
  transfer_duration int,
  basis text NOT NULL DEFAULT 'fare'
);
CREATE INDEX IF NOT EXISTS idx_fare_attributes_currency ON fare_attributes(currency);

CREATE TABLE IF NOT EXISTS fare_rules (
  fare_id text NOT NULL REFERENCES fare_attributes(fare_id) ON DELETE CASCADE,
  route_id bigint REFERENCES routes(id) ON DELETE CASCADE,
  origin_zone text REFERENCES zones(zone_id) ON DELETE SET NULL,
  destination_zone text REFERENCES zones(zone_id) ON DELETE SET NULL,
  contains_zone text REFERENCES zones(zone_id) ON DELETE SET NULL,
  PRIMARY KEY (fare_id, route_id, origin_zone, destination_zone)
);
CREATE INDEX IF NOT EXISTS idx_fare_rules_route ON fare_rules(route_id);
CREATE INDEX IF NOT EXISTS idx_fare_rules_origin ON fare_rules(origin_zone);
CREATE INDEX IF NOT EXISTS idx_fare_rules_dest ON fare_rules(destination_zone);

CREATE TABLE IF NOT EXISTS stop_zones (
  stop_id bigint NOT NULL REFERENCES stops_canonical(id) ON DELETE CASCADE,
  zone_id text NOT NULL REFERENCES zones(zone_id) ON DELETE CASCADE,
  PRIMARY KEY (stop_id, zone_id)
);
CREATE INDEX IF NOT EXISTS idx_stop_zones_zone ON stop_zones(zone_id);

-- === B+ per-region GTFS, import_logs, jobs/outbox, hysteresis, route_regions (merged from 002) ===
-- 002_bplus.sql — B+ per-region GTFS, import_logs, jobs/outbox, hysteresis, route_regions
-- Соответствует docs/14-plan.md §3.6/3.8/3.10, §2 Фаза 4 per-region

-- route_regions M2M для межрегиональных рейсов (§2, §3.6)
CREATE TABLE IF NOT EXISTS route_regions (
  route_id bigint NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
  region_code text NOT NULL REFERENCES regions(code) ON DELETE CASCADE,
  PRIMARY KEY(route_id, region_code)
);
CREATE INDEX IF NOT EXISTS idx_route_regions_region ON route_regions(region_code);

CREATE INDEX IF NOT EXISTS idx_terminals_locked ON terminals(is_locked) WHERE is_locked;

-- jobs queue (переиспользуемая) (§3.10) — создаётся до import_logs из-за FK
CREATE TABLE IF NOT EXISTS jobs (
  id bigserial PRIMARY KEY,
  type text NOT NULL CHECK (type IN ('import_gtfs','sync_mintrans','sync_rail','notify','cleanup')),
  payload jsonb NOT NULL DEFAULT '{}'::jsonb,
  region text,
  state text NOT NULL CHECK (state IN ('pending','running','retry','done','dead')) DEFAULT 'pending',
  attempts int NOT NULL DEFAULT 0,
  next_run timestamptz NOT NULL DEFAULT now(),
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_jobs_state_next ON jobs(state, next_run) WHERE state IN ('pending','retry');
CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_jobs_type_payload ON jobs((payload->>'hash')) WHERE state IN ('pending','running') AND payload ? 'hash';

-- import_logs per-stop trace (§3.10)
CREATE TABLE IF NOT EXISTS import_logs (
  id bigserial PRIMARY KEY,
  job_id bigint REFERENCES jobs(id) ON DELETE SET NULL,
  entity_type text NOT NULL CHECK (entity_type IN ('place','terminal','stop','route','trip')),
  entity_id text NOT NULL,
  stage text NOT NULL CHECK (stage IN ('normalize','enrich','dedup','verify','canonical')),
  action text NOT NULL,
  confidence real,
  distance_m int,
  lev real,
  source text,
  at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_import_logs_job ON import_logs(job_id);
CREATE INDEX IF NOT EXISTS idx_import_logs_entity ON import_logs(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_import_logs_at ON import_logs(at);
-- партиционирование по at — в Фазе 6 ATTACH PARTITION, DDL совместим (PK включает at в будущем)

-- outbox transactional (§3.10) — многопотребительский, добавляется при втором подписчике
CREATE TABLE IF NOT EXISTS outbox (
  id bigserial PRIMARY KEY,
  aggregate text NOT NULL,
  aggregate_id text NOT NULL,
  event text NOT NULL,
  payload jsonb NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON outbox(aggregate, aggregate_id);

-- view для review списком (§3.10)
CREATE OR REPLACE VIEW v_review_stops AS
SELECT rq.entity_type, rq.entity_id, rq.reason, rq.score, rq.created_at,
       t.geom, t.is_locked, tn.name as terminal_name, ti.code as identifier_code,
       (SELECT il.stage || ':' || il.action FROM import_logs il WHERE il.entity_id = rq.entity_id::text ORDER BY il.at DESC LIMIT 1) as last_stage
FROM review_queue rq
LEFT JOIN terminals t ON rq.entity_type='terminal' AND t.id = rq.entity_id
LEFT JOIN terminal_names tn ON tn.terminal_id = t.id AND tn.lang='ru'
LEFT JOIN terminal_identifiers ti ON ti.terminal_id = t.id AND ti.is_primary = true;

-- карантин legacy-дефектов (§7 ревью): lat=0 или дубли 0м — сразу в review_queue (разовый скрипт, не триггер)
-- INSERT INTO review_queue(entity_type, entity_id, reason, score)
-- SELECT 'terminal', id, 'legacy_missing_coords', 0 FROM terminals WHERE ST_Y(geom::geometry)=0 AND ST_X(geom::geometry)=0 ON CONFLICT DO NOTHING;

