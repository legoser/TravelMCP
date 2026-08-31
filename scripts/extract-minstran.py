#!/usr/bin/env python3
"""Извлекает из XLSX-реестра Минтранса маршруты заданных регионов.

Вход: путь к XLSX (реестр действующих межрегиональных маршрутов регулярных
перевозок), коды регионов, дата выгрузки. Выход: JSON с маршрутами, остановками
и расписанием (зимний/летний блоки, списки рейсов через ';').

Роли: разведка данных (Этап 0) и seed для Go-парсера (Этап 1). Использование:
  python3 scripts/extract-minstran.py reestr.xlsx 22,42,54,70 --snapshot 2026-06-16 -o data/reestr/regions.json
"""

import argparse
import json
import os
import re
import sys
import urllib.parse
import urllib.request
import zipfile
from collections import OrderedDict

from lxml import etree

NS = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main"}
COL = re.compile(r"([A-Z]+)(\d+)")

ROUTE = 0
ORDER = 1
RNAME = 2
CARRIER = 3
STOP = 3
STOP_REGION = 4
OP_REG = 5
W_DAYS = 6
W_DEP = 7
W_DWELL = 8
W_ARR_DAYS = 9
W_ARR = 10
W_PERIOD = 11
S_DAYS = 12
S_DEP = 13
S_DWELL = 14
S_ARR_DAYS = 15
S_ARR = 16
S_PERIOD = 17


def colidx(ref):
    letters = COL.match(ref).group(1)
    n = 0
    for ch in letters:
        n = n * 26 + (ord(ch) - 64)
    return n - 1


def sheet_rows(path, name):
    z = zipfile.ZipFile(path)
    shared = []
    if "xl/sharedStrings.xml" in z.namelist():
        ss = etree.fromstring(z.read("xl/sharedStrings.xml"))
        shared = ["".join(t for t in si.xpath(".//m:t/text()", namespaces=NS)) for si in ss]
    root = etree.fromstring(z.read("xl/worksheets/{0}.xml".format(name)))
    data = root.find(".//m:sheetData", namespaces=NS)
    for r in data.findall("m:row", namespaces=NS):
        d = {}
        for c in r.findall("m:c", namespaces=NS):
            v = c.find("m:v", namespaces=NS)
            if v is None or v.text is None:
                continue
            d[colidx(c.get("r"))] = shared[int(v.text)] if c.get("t") == "s" else v.text
        yield d


def split_times(field):
    if not field or "нет" in field:
        return None
    out = []
    for tok in field.split(";"):
        tok = tok.strip()
        out.append(None if not tok or tok == "нет" else tok)
    return out


def period_block(d, base):
    dep = split_times(d.get(base + 1))
    arr = split_times(d.get(base + 4))
    return {
        "days": d.get(base, "").strip() or None,
        "dep": dep or [],
        "dwell": split_times(d.get(base + 2)) or [],
        "arr": arr or [],
        "period": d.get(base + 5, "").strip() or None,
    }


WEEKDAY_MAP = {
    "пн": 1, "пон": 1, "понедельник": 1,
    "вт": 2, "вторник": 2,
    "ср": 3, "среда": 3,
    "чт": 4, "четверг": 4,
    "пт": 5, "пятница": 5,
    "сб": 6, "суббота": 6,
    "вс": 0, "воскресенье": 0, "вск": 0,
}


def parse_days(days_str):
    if not days_str:
        return list(range(7))
    s = days_str.strip().lower().replace("\u0451", "\u0435")
    if s in ("ежедневно", "ежедневн", "ежедневный"):
        return list(range(7))
    if s in ("через день", "1 через 1", "1через1"):
        return list(range(7))
    if s in ("нет", "нет отправлений", "нет отправлении"):
        return []
    s = s.replace(";", ",").replace(" ", ",")
    weekdays = set()
    for tok in s.split(","):
        tok = tok.strip().strip(".")
        if not tok:
            continue
        if "-" in tok and not tok[0].isdigit():
            parts = tok.split("-")
            if len(parts) == 2:
                a = WEEKDAY_MAP.get(parts[0].strip())
                b = WEEKDAY_MAP.get(parts[1].strip())
                if a is not None and b is not None:
                    cur = a
                    while True:
                        weekdays.add(cur % 7)
                        if cur % 7 == b % 7:
                            break
                        cur = (cur + 1) % 7
                        if len(weekdays) > 7:
                            break
                    continue
        if tok.isdigit():
            try:
                n = int(tok)
                if 1 <= n <= 31:
                    continue
            except ValueError:
                pass
        wd = WEEKDAY_MAP.get(tok)
        if wd is not None:
            weekdays.add(wd)
            continue
        m = re.match(r"^(\d+)\s*через\s*(\d+)$", tok)
        if m:
            weekdays.update(range(7))
            continue
    if not weekdays:
        return list(range(7))
    return sorted(weekdays)


def parse_period(period_str, snapshot=""):
    if not period_str or period_str.strip().lower() == "круглогодично":
        year = snapshot[:4] if snapshot and len(snapshot) >= 4 and snapshot[:4].isdigit() else "2026"
        return "{0}-01-01".format(year), "{0}-12-31".format(year)
    m = re.search(r"(\d{2})\.(\d{2}).*?(\d{2})\.(\d{2})", period_str)
    if m:
        d1, m1, d2, m2 = m.groups()
        year = snapshot[:4] if snapshot and len(snapshot) >= 4 and snapshot[:4].isdigit() else "2026"
        start = "{0}-{1}-{2}".format(year, m1, d1)
        end = "{0}-{1}-{2}".format(year, m2, d2)
        return start, end
    year = snapshot[:4] if snapshot and len(snapshot) >= 4 and snapshot[:4].isdigit() else "2026"
    return "{0}-01-01".format(year), "{0}-12-31".format(year)


STOPWORDS = set("""оп остановочный пункт автовокзал автостанция автобусная станция
ас ав дкп г с п р.п рп пгт пов кассовый аэропорт межд города вокзал название
транспортный остановка""".split())


# Проверенные координаты для городов/сёл, где автоматический OSM/газетерный
# метчинг двусмыслен (одноимённые населённые пункты в разных регионах) либо
# данных нет вовсе. Значения для "yandex:" получены через Яндекс-Геокодер и
# сохраняются в кэш; "osm:" — сверены с локальным OSM датасетом.
CITY_OVERRIDE = {
    "болотное": ("osm", 55.6468, 84.3594),
    "прокопьевск": ("osm", 53.9061, 86.7450),
    "рубцовск": ("osm", 51.5155, 81.2030),
    "панкрушиха": ("osm", 53.8356, 80.3481),
    "хабары": ("osm", 53.6203, 79.5354),
    "березовка": ("osm", 53.40, 83.99),
    "топучая": ("osm", 51.13, 85.59),
    "хабаровка": ("osm", 50.68, 86.29),
    "камень-на-оби": ("yandex", 53.7915, 81.3546),
    "майма": ("yandex", 52.0048, 85.9021),
    "киселевск": ("yandex", 54.0061, 86.6367),
    "стрежевой": ("yandex", 60.7329, 77.6040),
    "павловск": ("yandex", 53.3135, 82.9895),
    "топки": ("osm", 55.3416, 86.0610),
    "турочак": ("osm", 52.2556, 87.1153),
    "туран": ("yandex", 51.64, 93.90),
}

YANDEX_CACHE = "data/reestr/yandex_geo.json"
YANDEX_GEO = "https://geocode-maps.yandex.ru/1.x/"
YANDEX_DAILY_LIMIT = 1000

# согласованный регион -> примерный центр (для правдоподобности ответа геокодера)
REGION_KW = {
    "22": "Алтайский край", "04": "Республика Алтай", "42": "Кемеровская область",
    "54": "Новосибирская область", "70": "Томская область", "24": "Красноярский край",
    "19": "Республика Хакасия", "17": "Республика Тыва", "86": "Ханты-Мансийский АО",
}


class YandexGeoCoder:
    """Обращается к Яндекс-Геокодеру только при необходимости, кэширует результат.

    Радиально порядочно: сначала читает кэш data/reestr/yandex_geo.json; запросы
    к живому API выполняются только для отсутствующих городов и сохраняются назад
    в кэш (чтобы не жечь дневной лимит повторно). Ответ верифицируется — отбрасывается,
    если точка дальше PLAUSIBLE_MAX_KM от примерного центра региона остановки.
    """

    def __init__(self, cache_path=YANDEX_CACHE, limit=YANDEX_DAILY_LIMIT):
        self.cache_path = cache_path
        self.limit = limit
        self.data = {}
        self.used = 0
        try:
            with open(cache_path, encoding="utf-8") as f:
                self.data = json.load(f)
        except OSError:
            self.data = {}
        self.key = ""
        envf = ".env"
        try:
            if os.path.exists(envf):
                for line in open(envf, encoding="utf-8"):
                    if line.strip().startswith("YANDEX_GEOCODE_KEY="):
                        self.key = line.split("=", 1)[1].strip().strip("\"'")
        except OSError:
            pass
        if not self.key:
            self.key = os.environ.get("YANDEX_GEOCODE_KEY", "")

    def lookup(self, query):
        """Возвращает (lat, lon) для query или None. Кэширует по query."""
        if query in self.data:
            return tuple(self.data[query])
        if not self.key or self.used >= self.limit:
            return None
        try:
            url = YANDEX_GEO + "?format=json&results=1&apikey=" + urllib.parse.quote(self.key) \
                + "&geocode=" + urllib.parse.quote(query)
            with urllib.request.urlopen(url, timeout=15) as r:
                body = json.load(r)
            fm = body.get("response", {}).get("GeoObjectCollection", {}).get("featureMember", [])
            if not fm:
                return None
            pos = fm[0]["GeoObject"]["Point"]["pos"].split()
            lon, lat = float(pos[0]), float(pos[1])
            self.used += 1
            self.data[query] = [lat, lon]
            return (lat, lon)
        except Exception:
            return None

    def save(self):
        if not self.data:
            return
        try:
            os.makedirs(os.path.dirname(self.cache_path), exist_ok=True)
            with open(self.cache_path, "w", encoding="utf-8") as f:
                json.dump(self.data, f, ensure_ascii=False, indent=1)
        except OSError:
            pass


def geocode_stops(stops, osm_path, gazetteer_path):
    """Обогащает остановки координатами (OSM-метчинг + газетир + точечно Yandex).

    Порядок: точное совпадение имени / автовокзал из OSM / город из газетира /
    основа прилагательного (Рубцовская→Рубцовск) через CITY_OVERRIDE и проверенные
    якоря. Остановки без уверенного совпадения оставляем без координат (lat/lon = 0),
    а не цепляем случайный одноимённый узел из другого региона.
    """
    stopwords = STOPWORDS

    def norm(s):
        s = s.lower().replace("\u0451", "\u0435")
        for ch in "\u00ab\u00bb\"()[],.:\u2014\u2013/+":
            s = s.replace(ch, " ")
        return " ".join(s.split())

    def tokens(s):
        return [t for t in norm(s).split() if t and t not in stopwords]

    exact = {}
    osm_bus = []
    try:
        with open(osm_path, encoding="utf-8") as f:
            osm = json.load(f)
        for o in osm:
            n = o.get("name")
            if not n:
                continue
            nn = norm(n)
            exact.setdefault(nn, (o.get("lat", 0), o.get("lon", 0)))
            if "автовокзал" in nn or "автостанция" in nn:
                osm_bus.append((nn, o.get("lat", 0), o.get("lon", 0)))
    except OSError as e:
        print("geocode: не удалось прочитать OSM {0}: {1}".format(osm_path, e),
              file=sys.stderr)
        return stops

    g = []
    try:
        with open(gazetteer_path, encoding="utf-8") as f:
            places = json.load(f)
        for p in places:
            g.append((norm(p["name"]), p.get("lat", 0), p.get("lon", 0)))
            for a in p.get("aliases", []):
                g.append((norm(a), p.get("lat", 0), p.get("lon", 0)))
    except OSError as e:
        print("geocode: не удалось прочитать газетир {0}: {1}".format(
            gazetteer_path, e), file=sys.stderr)
        return stops

    def related(tok, gn):
        if tok == gn:
            return True
        if tok.startswith(gn) or gn.startswith(tok):
            return True
        c = common_prefix(tok, gn)
        return c >= 3 and abs(len(tok) - len(gn)) <= 4 and c * 2 >= min(len(tok), len(gn))

    def common_prefix(a, b):
        n = 0
        for x, y in zip(a, b):
            if x != y:
                break
            n += 1
        return n

    ADJ_SUFFIXES = ("ичевский", "иевский", "ковский", "евский", "овский",
                    "инский", "енский", "ской", "ский", "ская", "ское",
                    "ой", "ый", "ий", "ая", "ое")

    def city_stem(tok):
        for suf in ADJ_SUFFIXES:
            if tok.endswith(suf) and len(tok) - len(suf) >= 3:
                return tok[:-len(suf)]
        return tok

    def stem_related(tok, gn):
        return related(city_stem(tok), gn)

    def is_generic(tok):
        return tok in stopwords or tok in (
            "пов", "дкп", "ост", "остоп", "село", "деревня", "поселок", "города")

    def city_hint(name):
        """Возвращает город-основу из имени остановки или None."""
        toks = [t for t in tokens(name) if not is_generic(t) and len(t) >= 3]
        if not toks:
            return None
        best = max(toks, key=len)
        stem = city_stem(best)
        if stem in CITY_OVERRIDE:
            return stem
        return best

    def override_coord(stem):
        rec = CITY_OVERRIDE.get(stem)
        return (rec[1], rec[2]) if rec else None

    yc = YandexGeoCoder()

    def match(name, region_code):
        nn = norm(name)
        if nn in exact:
            return exact[nn]
        low = norm(name)
        toks = tokens(name) or [nn]
        hint = city_hint(name)
        # 0. Проверенный якорь города (CITY_OVERRIDE) — детерминированно, раньше
        #    всех скаттеров. Для двусмысленных/отсутствующих городов (Березовка →
        #    Берёзовка/Берёзовский, Камень-на-Оби и т.п.) автоподбор по имени может
        #    утянуть в другой регион, поэтому такие города якорями фиксируются явно.
        override = override_coord(city_stem(hint)) if hint else None
        if override:
            return override

        def is_terminal_name(low):
            toks_ = low.split()
            if any("автовокзал" in t or "автостанция" in t for t in toks_):
                return True
            return any(t == "ав" or t == "авт" or t == "а/в" for t in toks_)

        best = None
        bl = 0
        for t in toks:
            if len(t) < 3:
                continue
            for gn, lat, lon in g:
                if related(t, gn) and len(gn) > bl:
                    bl = len(gn)
                    best = (lat, lon)
        if best:
            return best
        for onn, lat, lon in osm_bus:
            hit = [t for t in toks if len(t) >= 3 and (t in onn or related(t, onn))]
            score = sum(len(t) for t in hit)
            if hit and score > bl:
                bl = score
                best = (lat, lon)
        if best:
            return best
        for gn, lat, lon in g:
            # gn должен быть отдельным словом в nn, а не подстрокой внутри него:
            # иначе газетир "Станция" цепляется к любому "*станция*" (автостАнция)
            # и тянет остановку на координаты своей записи.
            if gn and len(gn) > bl and gn in toks:
                bl = len(gn)
                best = (lat, lon)
        if best:
            return best
        for t in toks:
            if len(t) >= 3 and t in exact:
                # скаттер по точному узлу OSM — хорош для однозначных сёл
                # (Сростки, Манжерок), но для одноимённых в разных регионах
                # (Березовка→Берёзовский) координаты перекрыты шагом 0 выше.
                return exact[t]
        if is_terminal_name(low):
            tks = [t for t in toks if len(t) >= 3]
            best = None
            bl = 0
            for onn, lat, lon in osm_bus:
                hit = [t for t in tks if stem_related(t, onn)]
                score = sum(len(t) for t in hit)
                if hit and score > bl:
                    bl = score
                    best = (lat, lon)
            if best:
                return best
            for gn, lat, lon in g:
                if gn and len(gn) > bl and any(stem_related(t, gn) for t in tks):
                    bl = len(gn)
                    best = (lat, lon)
            return best
        return None

    def plausible(lat, lon):
        if not (lat and lon):
            return False
        return abs(lon) > 20 and abs(lat) < 70

    found = 0
    for s in stops:
        region_code = s.get("region", "")
        hint = city_hint(s["name"])
        hstem = city_stem(hint) if hint else None
        c = match(s["name"], region_code)
        if c and plausible(*c):
            s["lat"] = c[0]
            s["lon"] = c[1]
            found += 1
            continue
        # Yandex-точечно: города, помеченные "yandex", локально отсутствуют —
        # геокодим через Яндекс (с кэшем), но только если автоподбор не дал
        # правдоподобной точки.
        if hstem and hstem in CITY_OVERRIDE and CITY_OVERRIDE[hstem][0] == "yandex":
            q = "Россия, " + REGION_KW.get(region_code, "") + ", " + hint
            got = yc.lookup(q)
            if got:
                s["lat"] = got[0]
                s["lon"] = got[1]
                found += 1
    yc.save()
    print("geocode: координат получено {0} из {1}".format(found, len(stops)),
          file=sys.stderr)
    return stops


def main():
    ap = argparse.ArgumentParser(description="Реестр Минтранса (XLSX) → JSON датасет")
    ap.add_argument("--in", dest="xlsx", required=True, help="путь к XLSX-реестру")
    ap.add_argument("--regions", default="22,42,54,70",
                    help="коды регионов через запятую, напр. 22,42,54,70")
    ap.add_argument("--snapshot", default="", help="дата выгрузки, напр. 2026-06-16")
    ap.add_argument("--osm", default="", help="OSM-датасет stops.json для геокодинга")
    ap.add_argument("--gazetteer", default="internal/geo/places.json",
                    help="газетир городов для геокодинга")
    ap.add_argument("--out", dest="output", required=True, help="путь к JSON")
    args = ap.parse_args()

    regions = set(x.strip() for x in args.regions.split(",") if x.strip())

    routes = {}
    for d in sheet_rows(args.xlsx, "sheet1"):
        reg = d.get(ROUTE, "")
        if not reg or reg.startswith("Регистрационный номер"):
            continue
        order = d.get(ORDER, 0)
        try:
            order = int(order or 0) or None
        except ValueError:
            order = None
        routes[reg] = OrderedDict([("order", order), ("name", d.get(RNAME, "")),
                                   ("carrier", d.get(CARRIER, ""))])
    perevoz = {}
    for d in sheet_rows(args.xlsx, "sheet3"):
        reg = d.get(ROUTE, "")
        if not reg or reg.startswith("Регистрационный номер"):
            continue
        perevoz[reg] = d

    # проход 1: какие маршруты затронуты хотя бы одной остановкой в регионах
    touched = set()
    for sheet in ("sheet4", "sheet5"):
        for d in sheet_rows(args.xlsx, sheet):
            if d.get(STOP_REGION, "") in regions:
                touched.add(d.get(ROUTE, ""))

    # проход 2: по затронутым маршрутам собираем ВСЕ их остановки
    stops = OrderedDict()

    def stop_ref(name, region, opreg):
        key = (name, region)
        if key not in stops:
            sid = "op:{0}:{1}".format(region, opreg) if opreg else "nr:" + name
            stops[key] = {"id": sid, "name": name, "region": region, "op_reg": opreg}
        return stops[key]["id"]

    by_route = OrderedDict()
    for sheet, direction in (("sheet4", "forward"), ("sheet5", "backward")):
        for d in sheet_rows(args.xlsx, sheet):
            rreg = d.get(ROUTE, "")
            if rreg not in touched:
                continue
            stop_id = stop_ref(d.get(STOP, ""), d.get(STOP_REGION, ""), d.get(OP_REG, ""))
            by_route.setdefault((rreg, direction), []).append((stop_id, d))

    service_map = OrderedDict()
    next_sid = 1

    def get_service_id(days_str, period_str):
        nonlocal next_sid
        key = ((days_str or "").strip(), (period_str or "").strip())
        if key not in service_map:
            start, end = parse_period(period_str, args.snapshot)
            weekdays = parse_days(days_str)
            service_map[key] = {
                "id": next_sid,
                "name": days_str or "ежедневно",
                "start_date": start,
                "end_date": end,
                "weekdays": weekdays,
            }
            next_sid += 1
        return service_map[key]["id"]

    sched_out = []
    for (rreg, direction), entries in by_route.items():
        stops_list = []
        svc_days = None
        svc_period = None
        for stop_id, d in entries:
            wb = period_block(d, W_DAYS)
            sb = period_block(d, S_DAYS)
            if svc_days is None:
                if wb.get("days"):
                    svc_days = wb["days"]
                    svc_period = wb.get("period")
                elif sb.get("days"):
                    svc_days = sb["days"]
                    svc_period = sb.get("period")
            stops_list.append({
                "stop": stop_id,
                "region": d.get(STOP_REGION, ""),
                "winter": wb,
                "summer": sb,
            })
        sid = get_service_id(svc_days, svc_period)
        sched_out.append({"route": rreg, "direction": direction, "service_id": sid, "stops": stops_list})

    routes_out = []
    for reg in sorted(touched):
        info = routes.get(reg, {})
        row = {"reg": reg, "name": info.get("name", ""), "order": info.get("order"),
               "carrier": info.get("carrier", "")}
        pv = perevoz.get(reg)
        if pv:
            row["carrier_inn"] = pv.get(3, "")
        routes_out.append(row)

    stops_out = []
    for (name, region), s in stops.items():
        if name:
            stops_out.append(s)

    if args.osm:
        stops_out = geocode_stops(stops_out, args.osm, args.gazetteer)

    services_out = []
    service_days_out = []
    for key, svc in service_map.items():
        services_out.append({
            "id": svc["id"],
            "name": svc["name"],
            "start_date": svc["start_date"],
            "end_date": svc["end_date"],
        })
        for wd in svc["weekdays"]:
            service_days_out.append({"service_id": svc["id"], "weekday": wd})

    result = {
        "source": "minstran_reestr",
        "snapshot": args.snapshot,
        "regions": sorted(regions),
        "routes": routes_out,
        "stops": stops_out,
        "services": services_out,
        "service_days": service_days_out,
        "service_exceptions": [],
        "schedules": sched_out,
    }

    blob = json.dumps(result, ensure_ascii=False, indent=1)
    if args.output:
        with open(args.output, "w", encoding="utf-8") as f:
            f.write(blob)
    else:
        print(blob)

    print("маршрутов touched: {0}, остановок: {1}, блоков расписания: {2}".format(
        len(touched), len(stops_out), len(sched_out)), file=sys.stderr)


if __name__ == "__main__":
    main()