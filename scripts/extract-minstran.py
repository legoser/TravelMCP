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
import re
import sys
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


STOPWORDS = set("""оп остановочный пункт автовокзал автостанция автобусная станция
ас ав дкп г с п р.п рп пгт пов кассовый аэропорт межд города вокзал название
транспортный остановка""".split())


def geocode_stops(stops, osm_path, gazetteer_path):
    """Обогащает остановки координатами (OSM-метчинг + газетир).

    Координаты в реестре отсутствуют; метчинг по имени: автовокзалы из OSM,
    города/алиасы из газетира, префикс-совпадения для прилагательных.
    Остановки без совпадения оставляем без координат (lat/lon = 0).
    """
    stopwords = STOPWORDS
    lstops = [s.lower() for s in stopwords]

    def norm(s):
        s = s.lower()
        for ch in "\u00ab\u00bb\"()[],.:\u2014\u2013/+":
            s = s.replace(ch, " ")
        return " ".join(s.split())

    def tokens(s):
        return [t for t in norm(s).split() if t and t not in stopwords]

    def common_prefix(a, b):
        n = 0
        for x, y in zip(a, b):
            if x != y:
                break
            n += 1
        return n

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

    def match(name):
        nn = norm(name)
        if nn in exact:
            return exact[nn]
        toks = tokens(name) or [nn]
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
            if gn and gn in nn and len(gn) > bl:
                bl = len(gn)
                best = (lat, lon)
        if best:
            return best
        for t in toks:
            if t in exact and len(t) >= 3:
                return exact[t]
        return None

    found = 0
    for s in stops:
        c = match(s["name"])
        if c and abs(c[1]) > 20 and abs(c[0]) < 70:
            s["lat"] = c[0]
            s["lon"] = c[1]
            found += 1
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

    sched_out = []
    for (rreg, direction), entries in by_route.items():
        stops_list = []
        for stop_id, d in entries:
            stops_list.append({
                "stop": stop_id,
                "region": d.get(STOP_REGION, ""),
                "winter": period_block(d, W_DAYS),
                "summer": period_block(d, S_DAYS),
            })
        sched_out.append({"route": rreg, "direction": direction, "stops": stops_list})

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

    result = {
        "source": "minstran_reestr",
        "snapshot": args.snapshot,
        "regions": sorted(regions),
        "routes": routes_out,
        "stops": stops_out,
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