#!/usr/bin/env python3
"""Yandex Rasp -> GTFS.

Собирает GTFS-архив по выбранному региону из API Яндекс.Расписаний.
Поочерёдно обрабатывает регионы, кэширует станции, маршруты, расписания,
уважает лимит 500 запросов/сутки, пишет прогресс в лог.

Использование:
  YANDEX_RASP_KEY=xxx python3 scripts/yandex_rasp2gtfs_V2.py --region "Кемеровская область" --transport bus
  python3 scripts/yandex_rasp2gtfs_V2.py --region "Кемеровская область" --out data/gtfs --cache data/yandex/cache --limit 0

Кэш:
  data/yandex/cache/global_stations_list.json  (~40 МБ, переиспользуется)
  data/yandex/cache/schedule_<code>_<date>.json
  data/yandex/cache/thread_<uid>.json
  data/yandex/cache/quota_YYYY-MM-DD.json      (счётчик запросов)

Выход:
  data/gtfs/gtfs_<region>_<transport>.zip  или gtfs_output/gtfs_...zip
"""

import argparse
import csv
import datetime
import json
import logging
import os
import re
import sys
import time
import zipfile
from pathlib import Path

import requests

BASE_URL = "https://api.rasp.yandex.net/v3.0"
# Альтернативный домен из старого скрипта; оставляем fallback
BASE_URL_ALT = "https://api.rasp.yandex-net.ru/v3.0"

DEFAULT_REGION = "Кемеровская область"
DEFAULT_TRANSPORT = "bus"
DEFAULT_CACHE_DIR = Path("data/yandex/cache")
DEFAULT_GTFS_DIR = Path("data/gtfs")
FALLBACK_CACHE_DIR = Path("yandex_cache")
FALLBACK_GTFS_DIR = Path("gtfs_output")

TRANSPORT_TYPE_MAP = {
    "train": "2",
    "suburban": "2",
    "bus": "3",
    "water": "4",
    "plane": "11",
}

DAILY_LIMIT = 500
REQUEST_TIMEOUT = 30
RETRY_429_DELAY = 60

LOG_FMT = "%(asctime)s [%(levelname)s] %(message)s"


def setup_logging(log_file: Path | None, verbose: bool = False) -> None:
    level = logging.DEBUG if verbose else logging.INFO
    handlers: list[logging.Handler] = [logging.StreamHandler(sys.stdout)]
    if log_file is not None:
        log_file.parent.mkdir(parents=True, exist_ok=True)
        handlers.append(logging.FileHandler(log_file, encoding="utf-8"))
    logging.basicConfig(level=level, format=LOG_FMT, handlers=handlers)


def find_project_root(start: Path | None = None) -> Path:
    p = (start or Path(__file__).resolve()).parent
    for parent in [p, *p.parents]:
        if (parent / ".env").exists() or (parent / "go.mod").exists():
            return parent
    return Path.cwd()


def resolve_path(p: Path | str) -> Path:
    path = Path(p)
    if path.is_absolute():
        return path
    return find_project_root() / path


def load_dotenv(path: Path | None = None) -> None:
    if path is None:
        root = find_project_root()
        path = root / ".env"
        if not path.exists():
            alt = Path.cwd() / ".env"
            if alt.exists():
                path = alt
            else:
                return
    if not path.exists():
        return
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        if line.startswith("export "):
            line = line[len("export ") :]
        k, v = line.split("=", 1)
        k = k.strip()
        v = v.strip().strip("\"'").strip()
        if k and k not in os.environ:
            os.environ[k] = v


class QuotaTracker:
    def __init__(self, cache_dir: Path, limit: int = DAILY_LIMIT):
        self.cache_dir = resolve_path(cache_dir)
        self.cache_dir.mkdir(parents=True, exist_ok=True)
        self.limit = limit
        self.today = datetime.date.today().isoformat()
        self.path = self.cache_dir / f"quota_{self.today}.json"
        self.count = 0
        self._load()
        # гарантируем наличие файла чтобы не перезасходовать при проверке ls
        if not self.path.exists():
            self._save()

    def _load(self) -> None:
        if self.path.exists():
            try:
                data = json.loads(self.path.read_text(encoding="utf-8"))
                self.count = int(data.get("count", 0))
            except Exception:
                self.count = 0

    def _save(self) -> None:
        try:
            self.cache_dir.mkdir(parents=True, exist_ok=True)
            self.path.write_text(
                json.dumps({"date": self.today, "count": self.count, "updated": datetime.datetime.now().isoformat()}, ensure_ascii=False, indent=2),
                encoding="utf-8",
            )
        except Exception as e:
            logging.warning("не удалось сохранить квоту %s: %s", self.path, e)

    def can_request(self, n: int = 1) -> bool:
        return self.count + n <= self.limit

    def remaining(self) -> int:
        return max(0, self.limit - self.count)

    def consume(self, n: int = 1) -> None:
        self.count += n
        self._save()
        logging.info("квота: %d/%d использовано, осталось %d", self.count, self.limit, self.remaining())

    def assert_available(self) -> None:
        if self.count >= self.limit:
            raise RuntimeError(
                f"дневной лимит {self.limit} исчерпан ({self.count}). "
                f"Продолжите завтра — кэш сохранён в {self.cache_dir}"
            )


def sanitize_filename(s: str) -> str:
    s = re.sub(r"[^\w\-]+", "_", s.strip(), flags=re.UNICODE)
    s = re.sub(r"_+", "_", s).strip("_")
    return s or "region"


STOPWORDS = {"оп", "остановочный", "пункт", "автовокзал", "автостанция", "автобусная", "станция", "ас", "ав", "дкп", "г", "с", "п", "р.п", "рп", "пгт", "пов", "кассовый", "аэропорт", "город", "вокзал", "пос", "деревня", "село", "а", "и", "на"}


def norm_text(s: str) -> str:
    s = s.lower().replace("ё", "е")
    for ch in "«»\"'()[],.:—–/+\n\t":
        s = s.replace(ch, " ")
    return " ".join(s.split())


def tokens_set(s: str) -> set[str]:
    return {t for t in norm_text(s).split() if t and t not in STOPWORDS and len(t) >= 2}


def haversine_km(lat1: float, lon1: float, lat2: float, lon2: float) -> float:
    import math

    r = 6371.0
    dlat = (lat2 - lat1) * math.pi / 180
    dlon = (lon2 - lon1) * math.pi / 180
    a = math.sin(dlat / 2) ** 2 + math.cos(lat1 * math.pi / 180) * math.cos(lat2 * math.pi / 180) * math.sin(dlon / 2) ** 2
    return 2 * r * math.asin(math.sqrt(a))


def load_osm_index() -> list[dict]:
    for cand in [resolve_path("data/osm/stations.json"), resolve_path("internal/geo/places.json")]:
        if cand.exists():
            try:
                data = json.loads(cand.read_text(encoding="utf-8"))
                out: list[dict] = []
                for item in data:
                    name = item.get("name") or ""
                    lat = item.get("lat")
                    lon = item.get("lon")
                    if lat is None or lon is None:
                        continue
                    try:
                        lat_f = float(lat)
                        lon_f = float(lon)
                    except Exception:
                        continue
                    aliases = item.get("aliases") or []
                    if item.get("tags", {}).get("amenity") == "bus_station" or "bus_station" in str(item.get("tags")):
                        pass
                    out.append({"name": name, "aliases": aliases, "lat": lat_f, "lon": lon_f, "raw": item})
                if out:
                    logging.info("OSM индекс: %d объектов из %s", len(out), cand)
                    return out
            except Exception as e:
                logging.warning("OSM load %s: %s", cand, e)
    return []


def osm_match_by_name(stop_name: str, osm_index: list[dict]) -> dict | None:
    st_tokens = tokens_set(stop_name)
    if not st_tokens:
        return None
    best: dict | None = None
    best_score = -1
    for osm in osm_index:
        candidates = [osm["name"]] + osm.get("aliases", [])
        for cand in candidates:
            cand_tokens = tokens_set(cand)
            if not cand_tokens:
                continue
            inter = len(st_tokens & cand_tokens)
            if inter == 0:
                continue
            score = inter * 2 - len(cand_tokens) - len(st_tokens) * 0.1
            if cand_tokens == st_tokens:
                score += 10
            if score > best_score:
                best = osm
                best_score = score
    return best


def motis_geocode(text: str, base_url: str | None = None) -> tuple[float, float] | None:
    base_url = base_url or os.environ.get("MOTIS_URL") or os.environ.get("MOTIS_GEOCODER_URL") or ""
    if not base_url:
        for cand in ["http://localhost:8077", "http://localhost:8080"]:
            try:
                r = requests.get(f"{cand}/api/v1/geocode", params={"text": text}, timeout=3)
                if r.status_code == 200:
                    base_url = cand
                    break
            except Exception:
                continue
        if not base_url:
            return None
    try:
        r = requests.get(f"{base_url.rstrip('/')}/api/v1/geocode", params={"text": text}, timeout=5)
        if r.status_code != 200:
            return None
        data = r.json()
        feats = data.get("features") or data.get("results") or []
        if not feats:
            return None
        first = feats[0]
        coords = first.get("geometry", {}).get("coordinates") or first.get("centroid") or []
        if len(coords) >= 2:
            lon, lat = float(coords[0]), float(coords[1])
            return lat, lon
        props = first.get("properties", {})
        if "lat" in props and "lon" in props:
            return float(props["lat"]), float(props["lon"])
    except Exception as e:
        logging.debug("motis geocode %r: %s", text, e)
    return None


def motis_reverse(lat: float, lon: float, base_url: str | None = None) -> str | None:
    base_url = base_url or os.environ.get("MOTIS_URL") or ""
    if not base_url:
        for cand in ["http://localhost:8077", "http://localhost:8080"]:
            try:
                r = requests.get(f"{cand}/api/v1/reverse-geocode", params={"place": f"{lat},{lon}"}, timeout=3)
                if r.status_code == 200:
                    base_url = cand
                    break
            except Exception:
                continue
        if not base_url:
            return None
    try:
        r = requests.get(f"{base_url.rstrip('/')}/api/v1/reverse-geocode", params={"place": f"{lat},{lon}"}, timeout=5)
        if r.status_code != 200:
            return None
        data = r.json()
        if isinstance(data, dict) and "name" in data:
            return data["name"]
        feats = data.get("features") or []
        if feats:
            return feats[0].get("properties", {}).get("name")
    except Exception as e:
        logging.debug("motis reverse %s,%s: %s", lat, lon, e)
    return None


def osm_match(stop_name: str, lat: float | str, lon: float | str, osm_index: list[dict], threshold_km: float = 0.5) -> dict | None:
    try:
        slat = float(lat) if lat not in ("", None) else None
        slon = float(lon) if lon not in ("", None) else None
    except Exception:
        slat = slon = None
    if slat is None or slon is None:
        m = osm_match_by_name(stop_name, osm_index)
        if m:
            return m
        mg = motis_geocode(stop_name)
        if mg:
            return {"name": stop_name, "lat": mg[0], "lon": mg[1], "aliases": []}
        return None
    st_tokens = tokens_set(stop_name)
    if not st_tokens:
        return None
    best: dict | None = None
    best_dist = threshold_km + 1e-9
    best_name: str = ""
    for osm in osm_index:
        dist = haversine_km(slat, slon, osm["lat"], osm["lon"])
        if dist > threshold_km:
            continue
        candidates = [osm["name"]] + osm.get("aliases", [])
        for cand in candidates:
            cand_tokens = tokens_set(cand)
            if not cand_tokens:
                continue
            if st_tokens & cand_tokens or st_tokens == cand_tokens or cand_tokens.issubset(st_tokens) or st_tokens.issubset(cand_tokens):
                if dist < best_dist or (abs(dist - best_dist) < 1e-6 and len(cand) < len(best_name)):
                    best = osm
                    best_dist = dist
                    best_name = cand
                break
            if norm_text(cand) == norm_text(stop_name):
                if dist < best_dist:
                    best = osm
                    best_dist = dist
                    best_name = cand
                break
    return best


def fetch_with_cache(
    cache_path: Path,
    url: str,
    params: dict | None,
    quota: QuotaTracker | None = None,
    force_refresh: bool = False,
    retries: int = 2,
) -> dict | None:
    if cache_path.exists() and not force_refresh:
        logging.debug("кэш хит: %s", cache_path.name)
        try:
            return json.loads(cache_path.read_text(encoding="utf-8"))
        except Exception as e:
            logging.warning("битый кэш %s: %s — перезапросим", cache_path, e)

    if quota is not None:
        quota.assert_available()

    logging.info("запрос API: %s params=%s", url, {k: v for k, v in (params or {}).items() if k != "apikey"})

    last_err: str | None = None
    for attempt in range(retries + 1):
        try:
            resp = requests.get(url, params=params, timeout=REQUEST_TIMEOUT)
            if resp.status_code == 429:
                logging.warning("429 Too Many Requests (попытка %d/%d) — лимит Яндекса", attempt + 1, retries + 1)
                if attempt < retries:
                    time.sleep(RETRY_429_DELAY)
                    continue
                logging.error("429: %s", resp.text[:500])
                return None
            if resp.status_code == 404:
                logging.warning("404 Not Found %s params=%s — кэшируем пустой маркер чтобы не ретраить", url, {k: v for k, v in (params or {}).items() if k != "apikey"})
                try:
                    cache_path.parent.mkdir(parents=True, exist_ok=True)
                    cache_path.write_text(json.dumps({"_cached_404": True, "http_code": 404, "url": url}, ensure_ascii=False, indent=2), encoding="utf-8")
                except Exception:
                    pass
                return None
            if resp.status_code != 200:
                logging.error("ошибка API %d: %s", resp.status_code, resp.text[:1000])
                last_err = resp.text[:500]
                if resp.status_code >= 500 and attempt < retries:
                    time.sleep(2 * (attempt + 1))
                    continue
                return None
            data = resp.json()
            if quota is not None:
                quota.consume(1)
            cache_path.parent.mkdir(parents=True, exist_ok=True)
            cache_path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
            logging.debug("сохранён кэш %s (%d байт)", cache_path.name, cache_path.stat().st_size)
            return data
        except requests.exceptions.Timeout:
            last_err = "timeout"
            logging.warning("таймаут %s (попытка %d/%d)", url, attempt + 1, retries + 1)
            if attempt < retries:
                time.sleep(2 * (attempt + 1))
                continue
        except requests.exceptions.RequestException as e:
            last_err = str(e)
            logging.error("сбой сети %s: %s", url, e)
            if attempt < retries:
                time.sleep(2 * (attempt + 1))
                continue
        except Exception as e:
            logging.error("неожиданная ошибка %s: %s", url, e, exc_info=True)
            return None

    if last_err:
        logging.error("все попытки исчерпаны: %s", last_err)
    return None


def get_all_stations(api_key: str, cache_dir: Path, quota: QuotaTracker, force_refresh: bool = False) -> dict | None:
    logging.info("получение глобального списка станций (кэш ~40 МБ, один раз на все регионы)...")
    params = {"apikey": api_key, "format": "json", "lang": "ru_RU"}
    # Основной домен — api.rasp.yandex.net (как в yandex-collect.sh)
    cache_path = cache_dir / "global_stations_list.json"
    data = fetch_with_cache(cache_path, f"{BASE_URL}/stations_list/", params, quota=quota, force_refresh=force_refresh)
    if data is None and BASE_URL_ALT != BASE_URL:
        logging.warning("пробуем альтернативный домен %s", BASE_URL_ALT)
        data = fetch_with_cache(cache_path, f"{BASE_URL_ALT}/stations_list/", params, quota=quota, force_refresh=force_refresh)
    if data is None:
        logging.error("не удалось получить stations_list")
    else:
        countries = len(data.get("countries", []))
        logging.info("stations_list загружен: %d стран, кэш %s", countries, cache_path)
    return data


def load_terminals(path: Path | None = None) -> dict[str, bool]:
    if path is None:
        path = resolve_path("data/yandex/terminals.json")
    if not path.exists():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
        out: dict[str, bool] = {}
        for k, v in data.items():
            if k.startswith("_"):
                continue
            if isinstance(v, dict):
                out[k] = bool(v.get("is_terminal"))
            else:
                out[k] = bool(v)
        logging.info("терминалы: загружено %d записей из %s", len(out), path)
        return out
    except Exception as e:
        logging.warning("не удалось прочитать terminals %s: %s", path, e)
        return {}


def extract_region_data(stations_data: dict, region_name: str, transport_type: str, terminals: dict[str, bool] | None = None) -> list[dict]:
    logging.info("поиск региона '%s' (транспорт=%s)...", region_name, transport_type)
    stops: list[dict] = []
    target = region_name.strip().lower()
    if terminals is None:
        terminals = load_terminals()

    for country in stations_data.get("countries", []):
        if country.get("title") != "Россия":
            continue
        for region in country.get("regions", []):
            title = (region.get("title") or "").strip().lower()
            if target not in title and title not in target:
                if target != title:
                    continue
            logging.info("регион найден: '%s' (%d поселений)", region.get("title"), len(region.get("settlements", [])))
            for settlement in region.get("settlements", []):
                settlement_name = settlement.get("title") or ""
                for station in settlement.get("stations", []):
                    if station.get("transport_type") != transport_type:
                        continue
                    yandex_code = (station.get("codes") or {}).get("yandex_code")
                    if not yandex_code:
                        continue
                    if transport_type == "bus":
                        if terminals and yandex_code in terminals:
                            if not terminals[yandex_code]:
                                continue
                        elif terminals:
                            if station.get("station_type") != "bus_station":
                                continue
                        elif station.get("station_type") == "bus_stop":
                            continue
                    lat = station.get("latitude")
                    lon = station.get("longitude")
                    stops.append(
                        {
                            "stop_id": yandex_code,
                            "stop_name": f"{settlement_name}, {station.get('title')}" if settlement_name else station.get("title", ""),
                            "stop_lat": lat if lat not in (None, "") else "",
                            "stop_lon": lon if lon not in (None, "") else "",
                            "settlement": settlement_name,
                            "station_title": station.get("title", ""),
                        }
                    )
            seen: dict[str, dict] = {}
            for s in stops:
                seen[s["stop_id"]] = s
            deduped = list(seen.values())
            logging.info("станций отфильтровано: %d (уникальных %d) для %s/%s (терминалов %d)", len(stops), len(deduped), region_name, transport_type, len(deduped))
            if len(deduped) != len(stops):
                logging.debug("дубликаты удалены: %d", len(stops) - len(deduped))
            return deduped

    logging.warning("регион '%s' не найден. Проверьте написание (напр. 'Кемеровская область', 'Кемеровская область — Кузбасс')", region_name)
    # подсказка: показать доступные регионы
    available = []
    for country in stations_data.get("countries", []):
        if country.get("title") == "Россия":
            for r in country.get("regions", [])[:30]:
                available.append(r.get("title"))
    if available:
        logging.info("примеры регионов: %s ...", ", ".join(available[:10]))
    return []


def normalize_gtfs_time(t: str | None) -> str:
    if not t or not isinstance(t, str):
        return ""
    t = t.strip()
    if not t:
        return ""
    # Яндекс отдаёт "HH:MM" или "HH:MM:SS" или "YYYY-MM-DD HH:MM:SS"
    if " " in t:
        t = t.split()[-1]
    if len(t) == 5 and re.match(r"^\d{2}:\d{2}$", t):
        t += ":00"
    if re.match(r"^\d{2}:\d{2}:\d{2}$", t):
        return t
    # попытка распарсить
    m = re.match(r"^(\d{1,2}):(\d{2})(?::(\d{2}))?$", t)
    if m:
        h, mm, ss = m.groups()
        return f"{int(h):02d}:{mm}:{ss or '00'}"
    return ""


def collect_routes_and_trips(
    stops: list[dict],
    transport_type: str,
    api_key: str,
    cache_dir: Path,
    quota: QuotaTracker,
    date_str: str,
    limit_stations: int = 0,
    skip_no_coords: bool = False,
    max_threads_per_station: int = 0,
) -> tuple[list[dict], list[dict], list[dict], list[tuple[str, str, str]], dict[str, dict]]:
    logging.info(
        "сбор маршрутов/рейсов: %d станций, дата=%s, лимит_станций=%s, max_threads/станцию=%s",
        len(stops),
        date_str,
        limit_stations or "все",
        max_threads_per_station or "все",
    )

    routes: dict[str, dict] = {}
    trips: list[dict] = []
    stop_times: list[dict] = []
    calendar_dates: set[tuple[str, str, str]] = set()
    # для stops.txt — пополняем остановками из thread
    extra_stops: dict[str, dict] = {}

    sample_stops = stops[:limit_stations] if limit_stations and limit_stations > 0 else stops
    if limit_stations:
        logging.warning("демо-режим: берём только %d/%d станций (уберите --limit для полного сбора)", len(sample_stops), len(stops))
    else:
        logging.info("сканируем расписание для %d станций региона...", len(sample_stops))

    global_coords: dict[str, tuple[str, str]] = {}
    global_path = cache_dir / "global_stations_list.json"
    if global_path.exists():
        try:
            gj = json.loads(global_path.read_text(encoding="utf-8"))
            for country in gj.get("countries", []):
                for region in country.get("regions", []):
                    for settlement in region.get("settlements", []):
                        for st in settlement.get("stations", []):
                            code = (st.get("codes") or {}).get("yandex_code")
                            if code and st.get("latitude") and st.get("longitude"):
                                global_coords[code] = (str(st["latitude"]), str(st["longitude"]))
            logging.info("координаты из global_stations_list: %d кодов", len(global_coords))
        except Exception as e:
            logging.warning("не удалось построить карту координат из %s: %s", global_path, e)

    osm_index = load_osm_index()
    for s in stops:
        if not s.get("stop_lat") or not s.get("stop_lon"):
            m = osm_match(s.get("stop_name", ""), s.get("stop_lat"), s.get("stop_lon"), osm_index, threshold_km=0.7)
            if m:
                old = s["stop_name"]
                s["stop_name"] = m["name"]
                s["stop_lat"] = str(m["lat"])
                s["stop_lon"] = str(m["lon"])
                logging.info("OSM name-fallback терминал %s -> %s", old, m["name"])
        else:
            m = osm_match(s.get("stop_name", ""), s.get("stop_lat"), s.get("stop_lon"), osm_index, threshold_km=0.3)
            if m and tokens_set(s.get("stop_name", "")) == tokens_set(m["name"]):
                old = s["stop_name"]
                s["stop_name"] = m["name"]
                logging.debug("OSM exact терминал %s -> %s", old, m["name"])

    seen_threads: set[str] = set()
    total = len(sample_stops)

    for idx, stop in enumerate(sample_stops, 1):
        if skip_no_coords and not stop.get("stop_lat"):
            logging.debug("[%d/%d] пропуск без координат: %s", idx, total, stop["stop_name"])
            continue

        stop_id = stop["stop_id"]
        logging.info("[%d/%d] расписание станции: %s (%s) осталось квоты %d", idx, total, stop["stop_name"], stop_id, quota.remaining())

        if not quota.can_request(1):
            logging.error("лимит %d исчерпан — прерываем сбор, прогресс сохранён в кэше %s", quota.limit, cache_dir)
            break

        schedule_cache = cache_dir / f"schedule_{stop_id}_{date_str}.json"
        sched_params = {
            "apikey": api_key,
            "station": stop_id,
            "transport_types": transport_type,
            "date": date_str,
            "lang": "ru_RU",
        }
        sched_data = fetch_with_cache(schedule_cache, f"{BASE_URL}/schedule/", sched_params, quota=quota)
        if sched_data is None:
            # fallback домен
            sched_data = fetch_with_cache(schedule_cache, f"{BASE_URL_ALT}/schedule/", sched_params, quota=quota)
        if not sched_data or "schedule" not in sched_data:
            logging.debug("нет расписания для %s", stop_id)
            continue

        schedule = sched_data.get("schedule") or []
        logging.info("  найдено %d рейсов на %s", len(schedule), stop_id)
        if max_threads_per_station and len(schedule) > max_threads_per_station:
            logging.warning("  ограничиваем до %d ниток для экономии квоты (из %d)", max_threads_per_station, len(schedule))
            schedule = schedule[:max_threads_per_station]

        pending = []
        for item in schedule:
            thread = item.get("thread") or {}
            thread_id = thread.get("uid")
            if not thread_id or thread_id in seen_threads:
                continue
            pending.append(item)

        if max_threads_per_station and len(pending) > max_threads_per_station:
            cached_first = [it for it in pending if (cache_dir / f"thread_{it['thread']['uid']}.json").exists()]
            new_first = [it for it in pending if it not in cached_first]
            selected = (cached_first + new_first)[:max_threads_per_station]
            if len(selected) < len(pending):
                logging.info("  выбрано %d ниток из %d (кэш %d, новых %d) для %s", len(selected), len(pending), len(cached_first), len(new_first), stop_id)
            pending = selected
        elif pending and max_threads_per_station:
            logging.debug("  ниток к обработке: %d (лимит %d)", len(pending), max_threads_per_station)

        for item in pending:
            thread = item.get("thread") or {}
            thread_id = thread.get("uid")
            if not thread_id:
                continue

            route_num = (thread.get("number") or "").strip()
            route_title = (thread.get("title") or "").strip()
            if route_num:
                route_id = sanitize_filename(route_num)
                route_short = route_num
                route_long = route_title or route_num
            elif route_title:
                route_id = sanitize_filename(route_title)
                route_short = route_title
                route_long = route_title
            else:
                route_id = sanitize_filename(thread_id[:16])
                route_short = route_id
                route_long = route_id
            if route_id not in routes:
                routes[route_id] = {
                    "route_id": route_id,
                    "route_short_name": route_short,
                    "route_long_name": route_long,
                    "route_type": TRANSPORT_TYPE_MAP.get(transport_type, "3"),
                }

            service_id = f"service_{thread_id}"
            # Яндекс: item.days / item.date — не GTFS, поэтому берём date_str как базовую дату
            # если есть точная дата в item.date — используем её
            raw_date = item.get("date") or date_str
            # raw_date может быть "2026-09-04"
            gtfs_date = re.sub(r"-", "", raw_date[:10]) if "-" in raw_date else date_str.replace("-", "")
            if len(gtfs_date) != 8:
                gtfs_date = date_str.replace("-", "")
            calendar_dates.add((service_id, gtfs_date, "1"))

            # trips — дедуплицируем по thread_id (один thread может встречаться на разных станциях)
            if thread_id not in seen_threads:
                seen_threads.add(thread_id)
                trips.append(
                    {
                        "route_id": route_id,
                        "service_id": service_id,
                        "trip_id": thread_id,
                        "trip_headsign": thread.get("title") or route_num,
                    }
                )
            else:
                continue  # thread уже обработан — не запрашиваем повторно

            if not quota.can_request(1):
                logging.error("лимит исчерпан перед запросом thread %s — прерываем", thread_id)
                break

            logging.info("  └─ нитка %s (%s) ...", thread_id, thread.get("title") or route_num)
            thread_cache = cache_dir / f"thread_{thread_id}.json"
            thread_params = {"apikey": api_key, "uid": thread_id, "lang": "ru_RU"}
            thread_data = fetch_with_cache(thread_cache, f"{BASE_URL}/thread/", thread_params, quota=quota)
            if thread_data is None:
                thread_data = fetch_with_cache(thread_cache, f"{BASE_URL_ALT}/thread/", thread_params, quota=quota)
            if not thread_data or "stops" not in thread_data:
                logging.debug("    пустая нитка %s", thread_id)
                continue

            for seq, t_stop in enumerate(thread_data.get("stops") or [], 1):
                st = (t_stop.get("station") or {})
                code = st.get("code")
                if not code:
                    continue
                arrival = normalize_gtfs_time(t_stop.get("arrival"))
                departure = normalize_gtfs_time(t_stop.get("departure"))
                # GTFS требует хотя бы одно время
                if not arrival and not departure:
                    # некоторые остановки без времени — пропускаем или ставим заглушку
                    continue
                if not arrival:
                    arrival = departure
                if not departure:
                    departure = arrival

                stop_times.append(
                    {
                        "trip_id": thread_id,
                        "arrival_time": arrival,
                        "departure_time": departure,
                        "stop_id": code,
                        "stop_sequence": str(seq),
                    }
                )

                # пополняем справочник остановок для GTFS stops.txt
                if code not in extra_stops and code not in {s["stop_id"] for s in stops}:
                    lat = st.get("latitude") or st.get("lat") or ""
                    lon = st.get("longitude") or st.get("lng") or ""
                    if (not lat or not lon) and code in global_coords:
                        lat, lon = global_coords[code]
                    title = st.get("title") or st.get("station_title") or code
                    settlement = st.get("settlement") or st.get("settlement_title") or ""
                    name = f"{settlement}, {title}" if settlement else title
                    had_coords = bool(lat and lon)
                    if not lat or not lon:
                        osm = osm_match(name, lat, lon, osm_index, threshold_km=0.7)
                        if osm:
                            logging.debug("OSM name-fallback extra %s -> %s", name, osm["name"])
                            name = osm["name"]
                            lat = str(osm["lat"])
                            lon = str(osm["lon"])
                    else:
                        osm = osm_match(name, lat, lon, osm_index, threshold_km=0.3)
                        if osm and tokens_set(name) == tokens_set(osm["name"]):
                            logging.debug("OSM exact extra %s -> %s (%.2fкм)", name, osm["name"], haversine_km(float(lat), float(lon), osm["lat"], osm["lon"]))
                            name = osm["name"]
                    if not lat or not lon:
                        logging.warning("без координат: %s (%s) thread %s seq %d — нет в global (%s) и OSM не нашёл (проверь settlement/имя)", code, name, thread_id, seq, "есть" if code in global_coords else "нет")
                    extra_stops[code] = {
                        "stop_id": code,
                        "stop_name": name,
                        "stop_lat": lat if lat not in (None, "") else "",
                        "stop_lon": lon if lon not in (None, "") else "",
                    }

        # вежливая пауза чтобы не упереться в rate-limit Яндекса (не более 5 rps)
        time.sleep(0.2)

    logging.info(
        "сбор завершён: routes=%d trips=%d stop_times=%d calendar_dates=%d extra_stops=%d квота %d/%d",
        len(routes),
        len(trips),
        len(stop_times),
        len(calendar_dates),
        len(extra_stops),
        quota.count,
        quota.limit,
    )
    return list(routes.values()), trips, stop_times, list(calendar_dates), extra_stops


def write_gtfs_archive(
    stops: list[dict],
    routes: list[dict],
    trips: list[dict],
    stop_times: list[dict],
    calendar_dates: list[tuple[str, str, str]],
    extra_stops: dict[str, dict],
    out_dir: Path,
    zip_path: Path,
    region: str,
    transport: str,
) -> Path:
    logging.info("сборка GTFS: %s / %s -> %s", region, transport, zip_path)
    out_dir.mkdir(parents=True, exist_ok=True)
    zip_path.parent.mkdir(parents=True, exist_ok=True)

    # объединяем stops + extra_stops, дедуплицируем
    all_stops: dict[str, dict] = {s["stop_id"]: s for s in stops}
    for sid, s in extra_stops.items():
        if sid not in all_stops:
            all_stops[sid] = s
    all_stops_list = list(all_stops.values())
    logging.info("итого остановок в GTFS: %d (из региона %d + из ниток %d)", len(all_stops_list), len(stops), len(extra_stops))

    def save_csv(filename: str, headers: list[str], rows: list[dict], keys: list[str]) -> Path:
        path = out_dir / filename
        with open(path, "w", encoding="utf-8", newline="") as f:
            w = csv.writer(f, quoting=csv.QUOTE_MINIMAL)
            w.writerow(headers)
            for r in rows:
                w.writerow([r.get(k, "") for k in keys])
        logging.info("  %s: %d строк", filename, len(rows))
        return path

    agency_path = out_dir / "agency.txt"
    with open(agency_path, "w", encoding="utf-8", newline="") as f:
        w = csv.writer(f)
        w.writerow(["agency_id", "agency_name", "agency_url", "agency_timezone", "agency_lang"])
        w.writerow(["YANDEX", "Региональный оператор (Яндекс)", "https://rasp.yandex.ru", "Asia/Novokuznetsk", "ru"])

    save_csv("stops.txt", ["stop_id", "stop_name", "stop_lat", "stop_lon"], all_stops_list, ["stop_id", "stop_name", "stop_lat", "stop_lon"])
    save_csv("routes.txt", ["route_id", "route_short_name", "route_long_name", "route_type"], routes, ["route_id", "route_short_name", "route_long_name", "route_type"])
    save_csv("trips.txt", ["route_id", "service_id", "trip_id", "trip_headsign"], trips, ["route_id", "service_id", "trip_id", "trip_headsign"])
    save_csv(
        "stop_times.txt",
        ["trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence"],
        sorted(stop_times, key=lambda x: (x["trip_id"], int(x["stop_sequence"]))),
        ["trip_id", "arrival_time", "departure_time", "stop_id", "stop_sequence"],
    )

    # calendar_dates.txt — отдельные даты сервиса
    cal_path = out_dir / "calendar_dates.txt"
    with open(cal_path, "w", encoding="utf-8", newline="") as f:
        w = csv.writer(f)
        w.writerow(["service_id", "date", "exception_type"])
        for row in sorted(calendar_dates):
            w.writerow(row)
    logging.info("  calendar_dates.txt: %d строк", len(calendar_dates))

    # минимальная валидация GTFS перед упаковкой
    if not all_stops_list:
        logging.warning("GTFS без остановок — архив будет пустым")
    if not routes:
        logging.warning("GTFS без маршрутов — проверьте транспорт/регион")
    stop_ids = {s["stop_id"] for s in all_stops_list}
    missing_stops = {st["stop_id"] for st in stop_times} - stop_ids
    if missing_stops:
        logging.warning("в stop_times %d неизвестных stop_id (пример: %s)", len(missing_stops), list(missing_stops)[:5])

    files_to_pack = ["agency.txt", "stops.txt", "routes.txt", "trips.txt", "stop_times.txt", "calendar_dates.txt"]
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as z:
        for name in files_to_pack:
            p = out_dir / name
            if p.exists():
                z.write(p, name)
    logging.info("GTFS архив готов: %s (%d байт)", zip_path, zip_path.stat().st_size if zip_path.exists() else 0)
    return zip_path


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="Yandex Rasp -> GTFS (по регионам, с кэшем и лимитом 500/день)")
    p.add_argument("--region", default=DEFAULT_REGION, help=f"регион, напр. 'Кемеровская область' (default: {DEFAULT_REGION})")
    p.add_argument("--transport", default=DEFAULT_TRANSPORT, choices=list(TRANSPORT_TYPE_MAP.keys()), help="тип транспорта bus/train/suburban/plane/water")
    p.add_argument("--date", default="", help="дата расписания YYYY-MM-DD (default: сегодня)")
    p.add_argument("--cache-dir", default=str(DEFAULT_CACHE_DIR), help=f"директория кэша (default: {DEFAULT_CACHE_DIR})")
    p.add_argument("--out", dest="out_dir", default=str(DEFAULT_GTFS_DIR), help=f"директория для txt и zip (default: {DEFAULT_GTFS_DIR})")
    p.add_argument("--limit", type=int, default=0, help="демо-лимит станций (0 = все, напр. 5 для экономии квоты)")
    p.add_argument("--max-threads", type=int, default=5, help="макс. ниток на станцию (0=все, 5 демо для экономии квоты)")
    p.add_argument("--log", default="", help="файл лога (default: <out>/gtfs_builder.log)")
    p.add_argument("--force-refresh", action="store_true", help="игнорировать кэш stations_list и перезапросить")
    p.add_argument("--skip-no-coords", action="store_true", help="пропускать станции без координат")
    p.add_argument("--daily-limit", type=int, default=DAILY_LIMIT, help=f"дневной лимит запросов (default: {DAILY_LIMIT})")
    p.add_argument("--offline", action="store_true", help="офлайн: использовать data/yandex/raw/*.json без запросов к API")
    p.add_argument("--raw-dir", default="data/yandex/raw", help="директория с готовыми yandex json (для --offline)")
    p.add_argument("-v", "--verbose", action="store_true", help="debug лог")
    return p.parse_args()


def build_offline_from_raw(raw_dir: Path, region: str, transport: str, date_str: str, limit: int = 0) -> tuple[list[dict], list[dict], list[dict], list[dict], list[tuple[str, str, str]], dict[str, dict]]:
    raw_dir = Path(raw_dir)
    logging.info("офлайн режим: используем %s (регион=%s транспорт=%s)", raw_dir, region, transport)
    if not raw_dir.exists():
        logging.error("raw_dir не найден: %s", raw_dir)
        return [], [], [], [], [], {}

    # Собираем станции и расписания из schedule файлов (kemerovo.json, barnaul.json и т.д.)
    # nearest.json даёт координаты станций для stops.txt
    stops: list[dict] = []
    schedules: list[dict] = []

    # Карта регион -> файлы (эвристика по имени)
    region_hint = region.lower()
    # для Кемеровской ищем kemerovo.json / kemerovo.nearest.json
    for jf in sorted(raw_dir.glob("*.json")):
        if ".nearest" in jf.name or jf.name.startswith("nsk_"):
            continue
        try:
            data = json.loads(jf.read_text(encoding="utf-8"))
        except Exception as e:
            logging.warning("битый json %s: %s", jf, e)
            continue
        station = data.get("station") or {}
        code = station.get("code")
        title = station.get("title", "")
        lat = station.get("latitude") or station.get("lat") or ""
        lon = station.get("longitude") or station.get("lng") or ""
        # nearest файл может дать координаты точнее
        nearest = raw_dir / f"{jf.stem}.nearest.json"
        if not lat and nearest.exists():
            try:
                nd = json.loads(nearest.read_text(encoding="utf-8"))
                for st in nd.get("stations", []):
                    if st.get("code") == code:
                        lat = st.get("lat") or lat
                        lon = st.get("lng") or lon
                        break
            except Exception:
                pass
        # Фильтр по региону: для оффлайна берём только файлы подходящие под регион
        # Если регион Кемеровская — берём только kemerovo*
        is_match = False
        if "кемеров" in region_hint:
            is_match = "kemerovo" in jf.stem.lower() or "кемеров" in title.lower()
        elif "новосибир" in region_hint:
            is_match = "nsk" in jf.stem.lower() and "barnaul" not in jf.stem.lower() and "tomsk" not in jf.stem.lower() and "kemerovo" not in jf.stem.lower()
        elif "барнаул" in region_hint or "алтай" in region_hint:
            is_match = "barnaul" in jf.stem.lower()
        elif "томск" in region_hint:
            is_match = "tomsk" in jf.stem.lower() and jf.stem.lower() == "tomsk"
        else:
            is_match = True
        if not is_match:
            logging.debug("офлайн пропуск %s (не подходит под регион %s)", jf.name, region)
            continue
        if code:
            stops.append({"stop_id": code, "stop_name": title, "stop_lat": lat, "stop_lon": lon})
        if "schedule" in data:
            schedules.append(data)
            logging.info("  найден schedule %s: %s (%d рейсов)", jf.name, title, len(data.get("schedule", [])))

    if not stops:
        logging.warning("офлайн: не найдено станций для региона %s в %s", region, raw_dir)
        logging.info("доступные файлы: %s", ", ".join(p.name for p in raw_dir.glob("*.json")))
        return [], [], [], [], [], {}

    # Ограничим демо
    if limit and limit > 0:
        stops = stops[:limit]
        schedules = schedules[:limit]

    routes: dict[str, dict] = {}
    trips: list[dict] = []
    stop_times: list[dict] = []
    calendar_dates: set[tuple[str, str, str]] = set()
    extra_stops: dict[str, dict] = {}
    seen_threads: set[str] = set()

    for sched in schedules:
        for item in sched.get("schedule", [])[: (limit or 1000) if limit else 1000]:
            thread = item.get("thread") or {}
            thread_id = thread.get("uid")
            if not thread_id or thread_id in seen_threads:
                continue
            seen_threads.add(thread_id)
            route_num = thread.get("number") or thread_id[:12]
            route_id = re.sub(r"[^\w\-]+", "_", route_num.strip()) or "ROUTE_UNK"
            if route_id not in routes:
                routes[route_id] = {
                    "route_id": route_id,
                    "route_short_name": route_num,
                    "route_long_name": thread.get("title") or route_num,
                    "route_type": TRANSPORT_TYPE_MAP.get(transport, "3"),
                }
            service_id = f"service_{thread_id}"
            raw_date = item.get("date") or sched.get("date") or date_str
            gtfs_date = re.sub(r"-", "", raw_date[:10]) if "-" in raw_date else date_str.replace("-", "")
            if len(gtfs_date) != 8:
                gtfs_date = date_str.replace("-", "")
            calendar_dates.add((service_id, gtfs_date, "1"))
            trips.append({"route_id": route_id, "service_id": service_id, "trip_id": thread_id, "trip_headsign": thread.get("title") or route_num})

            # В оффлайне нет thread деталей — делаем минимальные stop_times (отправление со станции)
            dep = normalize_gtfs_time(item.get("departure"))
            if not dep:
                # пробуем из строки даты
                try:
                    dt = item.get("departure") or ""
                    if "T" in dt:
                        dep = normalize_gtfs_time(dt.split("T")[1][:8])
                except Exception:
                    dep = "00:00:00"
            if not dep:
                dep = "00:00:00"
            arr = normalize_gtfs_time(item.get("arrival")) or dep
            station_code = sched.get("station", {}).get("code") or ""
            if station_code:
                stop_times.append({"trip_id": thread_id, "arrival_time": arr, "departure_time": dep, "stop_id": station_code, "stop_sequence": "1"})
                # попробуем добавить конечную остановку из title "Кемерово — Томск" как вторую остановку
                title = thread.get("title") or ""
                if "—" in title or "-" in title:
                    parts = re.split(r"\s*[—\-]\s*", title)
                    if len(parts) == 2:
                        # эвристика: конечная станция не известна по коду, создаём псевдо-остановку
                        dest_name = parts[1].strip()
                        dest_id = f"dest_{sanitize_filename(dest_name)}"
                        if dest_id not in extra_stops and dest_id not in {s["stop_id"] for s in stops}:
                            extra_stops[dest_id] = {"stop_id": dest_id, "stop_name": dest_name, "stop_lat": "", "stop_lon": ""}
                        # добавляем вторую остановку через 3 часа (эвристика для демо)
                        try:
                            h, m, s = map(int, dep.split(":"))
                            total_s = h * 3600 + m * 60 + s + 3 * 3600
                            h2 = (total_s // 3600) % 24
                            m2 = (total_s % 3600) // 60
                            s2 = total_s % 60
                            arr2 = f"{h2:02d}:{m2:02d}:{s2:02d}"
                        except Exception:
                            arr2 = dep
                        stop_times.append({"trip_id": thread_id, "arrival_time": arr2, "departure_time": arr2, "stop_id": dest_id, "stop_sequence": "2"})

    logging.info("офлайн собрано: stops=%d routes=%d trips=%d stop_times=%d", len(stops), len(routes), len(trips), len(stop_times))
    return stops, list(routes.values()), trips, stop_times, list(calendar_dates), extra_stops


def main() -> None:
    load_dotenv()
    args = parse_args()

    cache_dir = resolve_path(args.cache_dir)
    out_dir = resolve_path(args.out_dir)
    # fallback если data/ не создана (тоже резолвим относительно корня)
    fallback_cache = resolve_path(FALLBACK_CACHE_DIR)
    fallback_out = resolve_path(FALLBACK_GTFS_DIR)
    if not cache_dir.exists() and fallback_cache.exists():
        cache_dir = fallback_cache
    if not out_dir.exists() and fallback_out.exists() and str(out_dir) == str(resolve_path(DEFAULT_GTFS_DIR)):
        pass

    # гарантируем существование каталогов до логирования/квоты
    cache_dir.mkdir(parents=True, exist_ok=True)
    out_dir.mkdir(parents=True, exist_ok=True)

    raw_log = Path(args.log) if args.log else out_dir / "gtfs_builder.log"
    log_file = resolve_path(raw_log) if not raw_log.is_absolute() else raw_log
    setup_logging(log_file, verbose=args.verbose)

    date_str = args.date.strip() or datetime.date.today().isoformat()
    try:
        datetime.date.fromisoformat(date_str)
    except ValueError:
        logging.error("неверный --date %r, ожидается YYYY-MM-DD", date_str)
        sys.exit(2)

    # Офлайн ветка — не требует ключа и квоты
    if args.offline:
        raw_dir = resolve_path(args.raw_dir)
        logging.info("старт ОФЛАЙН: регион='%s' транспорт=%s дата=%s raw=%s out=%s", args.region, args.transport, date_str, raw_dir, out_dir)
        stops_o, routes_o, trips_o, stop_times_o, calendar_o, extra_o = build_offline_from_raw(raw_dir, args.region, args.transport, date_str, limit=args.limit)
        if not stops_o:
            logging.error("офлайн: нет данных для '%s' — проверьте raw_dir %s", args.region, raw_dir)
            sys.exit(4)
        safe_region = sanitize_filename(args.region)
        zip_name = f"gtfs_{safe_region}_{args.transport}.zip"
        zip_path = out_dir / zip_name
        write_gtfs_archive(stops_o, routes_o, trips_o, stop_times_o, calendar_o, extra_o, out_dir, zip_path, args.region, args.transport)
        logging.info("офлайн готово. Архив %s — квота не тратилась", zip_path)
        return

    api_key = os.environ.get("YANDEX_RASP_KEY", "").strip().strip("\"'")
    if not api_key:
        logging.error("нет YANDEX_RASP_KEY в env/.env — без ключа запросы невозможны (кэш stations_list может спасти частично)")
        logging.error("экспортируйте: export YANDEX_RASP_KEY=xxx  или добавьте в .env")
        sys.exit(1)

    quota = QuotaTracker(cache_dir, limit=args.daily_limit)
    logging.info("квота сегодня %s: %d/%d использовано, осталось %d", quota.today, quota.count, quota.limit, quota.remaining())
    logging.info("старт: регион='%s' транспорт=%s дата=%s кэш=%s out=%s", args.region, args.transport, date_str, cache_dir, out_dir)

    try:
        raw_stations = get_all_stations(api_key, cache_dir, quota, force_refresh=args.force_refresh)
        if not raw_stations:
            # пробуем взять кэш без запроса если он есть
            cached = cache_dir / "global_stations_list.json"
            if cached.exists():
                logging.warning("берём stations_list из кэша без запроса: %s", cached)
                raw_stations = json.loads(cached.read_text(encoding="utf-8"))
            else:
                logging.error("не удалось получить список станций — нет кэша и API недоступен")
                sys.exit(3)

        filtered_stops = extract_region_data(raw_stations, args.region, args.transport)
        if not filtered_stops:
            logging.error("нет станций для региона '%s' транспорт=%s — проверьте написание региона", args.region, args.transport)
            sys.exit(4)

        routes, trips, stop_times, calendar, extra_stops = collect_routes_and_trips(
            filtered_stops,
            args.transport,
            api_key,
            cache_dir,
            quota,
            date_str,
            limit_stations=args.limit,
            skip_no_coords=args.skip_no_coords,
            max_threads_per_station=args.max_threads,
        )

        if not routes and not trips:
            logging.warning("маршруты не найдены — возможно лимит исчерпан или у станций нет рейсов на %s", date_str)

        safe_region = sanitize_filename(args.region)
        zip_name = f"gtfs_{safe_region}_{args.transport}.zip"
        zip_path = out_dir / zip_name
        # также дублируем в корень для совместимости со старым скриптом
        write_gtfs_archive(filtered_stops, routes, trips, stop_times, calendar, extra_stops, out_dir, zip_path, args.region, args.transport)

        logging.info("готово. Файлы txt в %s, архив %s", out_dir, zip_path)
        logging.info("кэш переиспользуется при следующем запуске; для следующего региона: --region 'Новосибирская область'")
        if quota.remaining() < 50:
            logging.warning("квота на исходе: осталось %d — планируйте следующий регион на завтра", quota.remaining())

    except RuntimeError as e:
        logging.error("%s", e)
        sys.exit(5)
    except KeyboardInterrupt:
        logging.warning("прервано пользователем, кэш сохранён")
        sys.exit(130)
    except Exception as e:
        logging.critical("критический сбой: %s", e, exc_info=True)
        sys.exit(10)


if __name__ == "__main__":
    main()
docker run --rm -v $(pwd)/input:/input -v $(pwd)/data:/data ghcr.io/motis-project/motis:latest motis config /input/russia-260901.osm.pbf /input/gtfs_kem_bus.zip