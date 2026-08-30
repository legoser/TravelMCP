#!/usr/bin/env python3
"""Генерация internal/geo/places.json — газетир населённых пунктов
для find_route по названию места (пока без внешнего геокодинга).
Источник: data/osm/stations.json (автовокзалы/автостанции/аэропорты).
"""

import json
import re
import sys
from pathlib import Path
 

ROOT = Path(__file__).resolve().parent.parent
OSM = ROOT / "data" / "osm" / "stations.json"
OUT = ROOT / "internal" / "geo" / "places.json"


ADJ_OVERRIDES = {
    "новосибирский": "Новосибирск",
    "мариинский": "Мариинск",
    "красноярский": "Красноярск",
    "омский": "Омск",
    "краснозёрская": "Краснозёрское",
    "ордынская": "Ордынское",
    "волчихинская": "Волчиха",
    "хилокская": "Хилок",
    "таштагольский": "Таштагол",
    "тогульский": "Тогул",
    "турочакский": "Турочак",
    "емельяновский": "Емельяново",
    "зеленогорский": "Зеленогорск",
    "калачинский": "Калачинск",
    "берёзовский": "Берёзовский",
}

CURATED = [
    {"name": "Кемерово", "aliases": ["Автовокзал Кемерово"], "lat": 55.3416, "lon": 86.0610},
    {"name": "Бийск", "aliases": ["Бийский автовокзал"], "lat": 52.5488, "lon": 85.2177},
    {"name": "Междуреченск", "aliases": ["Автовокзал Междуреченск"], "lat": 52.8965, "lon": 90.2115},
]


def toponym(raw):
    s = raw
    s = re.sub(r"-(Главный|Пригород|Южный|Западный|Северный|Восточный|Центральный|Кольцо|\d+)$", "", s, flags=re.I)  # ...-Главный/-2
    s = re.sub(r"[«»\"']", " ", s)
    s = re.sub(r"\((.*?)\)", r" \1 ", s)  # (г.Юрга) и т.п.
    s = re.sub(r"(?i)автовокзал|автостанция|аэровокзал|аэропорт|междугородн(ый|ий|ой)?|пригородны(й|й)?|местных линий|по требованию|здание нового терминала", " ", s)
    s = re.sub(r"[.,;/\"«»'()]+", " ", s)
    if not s:
        return None
    first = None
    for tk in re.split(r"\s+", s):
        if not tk:
            continue
        tk = tk.strip('"«»()')
        if tk.lower() in ("г", "с", "п", "р", "д", "к", "рп", "пос", "пгт", "у") or re.fullmatch(r"[сгп]\.", tk):
            continue
        first = tk
        break
    if not first or not re.match(r"[А-ЯЁ]", first[0]):
        return None
    low = first.lower()
    return ADJ_OVERRIDES.get(low.rstrip("ы"), low.capitalize() if low[0].islower() else first)


def rank(o):
    tags = o.get("tags") or {}
    name = (o.get("name") or "").lower()
    if "автовокзал" in name:
        return 0
    if "автостанция" in name:
        return 1
    if tags.get("amenity") == "bus_station":
        return 2
    if "аэропорт" in name:
        return 3
    return 4


def main():
    objects = json.loads(OSM.read_text())
    best = {}
    hubs = re.compile(r"(?i)(автовокзал|автостанция|аэровокзал|аэропорт)")
    for o in objects:
        name = o.get("name") or o.get("display_name") or ""
        if not name or not hubs.search(name):
            continue
        t = toponym(name)
        if not t:
            continue
        lat = o.get("lat") or o.get("latitude")
        lon = o.get("lon") or o.get("longitude")
        if lat is None or lon is None:
            continue
        r = rank(o)
        if t not in best or r < best[t][2]:
            best[t] = (float(lat), float(lon), r, name)

    places = []
    for name in sorted(best):
        lat, lon, _, raw = best[name]
        places.append({"name": name, "aliases": [raw], "lat": lat, "lon": lon})

    known = {p["name"] for p in places}
    for p in CURATED:
        if p["name"] not in known:
            places.append(p)

    places.sort(key=lambda p: p["name"])
    OUT.write_text(json.dumps(places, ensure_ascii=False, indent=2) + "\n")
    print(f"places: {len(places)} -> {OUT}")


if __name__ == "__main__":
    main()