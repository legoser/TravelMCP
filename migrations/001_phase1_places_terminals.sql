-- 001_phase1_places_terminals.sql — Фаза 1 (§3.1-3.3, §3.8-3.9)
-- Postgres + PostGIS, компилируется для prod. SQLite-версия — в internal/store/sqlite.go:Migrate

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
  name_en text
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_carriers_inn ON carriers(inn) WHERE inn IS NOT NULL;
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
-- GiST geom already indexed via geography; add bbox gist if needed
-- CREATE INDEX IF NOT EXISTS idx_places_geom ON places USING gist(geom);
-- CREATE INDEX IF NOT EXISTS idx_places_bbox ON places USING gist(bbox);

-- 3.3 терминалы и остановки
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
  last_verified_at timestamptz
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
  system text NOT NULL,
  code_type text NOT NULL,
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

CREATE TABLE IF NOT EXISTS review_queue (
  entity_type text NOT NULL CHECK (entity_type IN ('place','terminal','stop','route','trip')),
  entity_id bigint NOT NULL,
  reason text NOT NULL,
  score real,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (entity_type, entity_id)
);

-- триггеры §3.2 closure
CREATE OR REPLACE FUNCTION fn_rebuild_closure_subtree(p_root bigint) RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$
DECLARE
  r record;
BEGIN
  DELETE FROM place_closure WHERE descendant_id IN (SELECT descendant_id FROM place_closure WHERE ancestor_id = p_root);
  -- рекурсивно собрать поддерево
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
  -- fallback полный rebuild если пусто
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
  -- добавить транзитивные через WITH RECURSIVE если выше не покрыло
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

-- trigger osm_compatible_name §3.3
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

-- проверка консистентности closure vs parent_id
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

-- provenance orphan check trigger
CREATE OR REPLACE FUNCTION trg_provenance_no_orphan() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.entity_type='place' AND NOT EXISTS (SELECT 1 FROM places WHERE id=NEW.entity_id) THEN RAISE EXCEPTION 'provenance orphan place %', NEW.entity_id; END IF;
  IF NEW.entity_type='terminal' AND NOT EXISTS (SELECT 1 FROM terminals WHERE id=NEW.entity_id) THEN RAISE EXCEPTION 'provenance orphan terminal %', NEW.entity_id; END IF;
  IF NEW.entity_type='stop' AND NOT EXISTS (SELECT 1 FROM stops_canonical WHERE id=NEW.entity_id) THEN RAISE EXCEPTION 'provenance orphan stop %', NEW.entity_id; END IF;
  RETURN NEW;
END; $$;
DROP TRIGGER IF EXISTS trg_provenance_no_orphan ON provenance;
CREATE TRIGGER trg_provenance_no_orphan BEFORE INSERT OR UPDATE ON provenance FOR EACH ROW EXECUTE FUNCTION trg_provenance_no_orphan();
