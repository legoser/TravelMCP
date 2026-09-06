#!/usr/bin/env python3
"""Нарезка testdata/test.json из полного датасета реестра + координаты из yandex-дампа.

Фикстура — фиксированный артефакт (источник истины для unit-тестов),
регенерация — только вручную при смене снапшота реестра:

  python3 scripts/extract-minstran.py --in data/raw/minstran/reestr.xlsx \\
      --regions 22,54,70 --snapshot 2026-05-10 --out /tmp/full.json
  python3 scripts/make-test-fixture.py /tmp/full.json \\
      data/yandex/cache/global_stations_list.json testdata/test.json

Состав: 4 маршрута (54.22.078, 54.70.040, 54.22.049, 54.22.030) × forward/backward
= 8 рейсов; координаты остановок — из офлайн-дампа Яндекса (+5 ручных привязок
OVERRIDES ниже). Ожидания тестов: 4 routes / 8 trips, 54.22.078 forward 12:30→17:00
(7 стопов), backward 19:59→…, НСК→Томск через 54.70.040 (10:00→15:00).
"""
import json, re, sys, unicodedata

FULL = sys.argv[1] if len(sys.argv) > 1 else "/tmp/opencode/regions_full.json"
DUMP = sys.argv[2] if len(sys.argv) > 2 else "data/yandex/cache/global_stations_list.json"
OUT = sys.argv[3] if len(sys.argv) > 3 else "testdata/test.json"
ROUTES = ["54.22.078", "54.70.040", "54.22.049", "54.22.030"]

def norm(s):
    s = unicodedata.normalize("NFKC", s or "").lower().replace("ё", "е")
    s = re.sub(r"[«»\"'().,/-]", " ", s)
    s = re.sub(r"\s+", " ", s).strip()
    return s

STOPWORDS = {"оп", "ав", "ас", "дкп", "вокзал", "главный", "г", "рп", "с", "ао",
             "остановочный", "пункт", "станция", "платформа", "касса", "автостанция",
             "автовокзал", "остановка", "поворот"}

OVERRIDES = {
    "op:54:54106": ("Новосибирск, автостанция «Речной вокзал»", 55.008070590379944, 82.93647936902936),
    "op:54:54107": ("Новосибирск, автостанция «Речной вокзал»", 55.008070590379944, 82.93647936902936),
    "op:70:70016": ("Томск, автовокзал", 56.4612664816162, 84.9912958591767),
    "op:22:22163": ("Крутиха, Алтайский край", 53.965376395374825, 81.2134006640053),
    "op:22:22007": ("Камень-на-Оби, автовокзал", 53.7901957064213, 81.315934187835),
}

def tokens(s):
    return [t for t in norm(s).split() if t not in STOPWORDS and len(t) > 2]

def main():
    d = json.load(open(FULL))
    dump = json.load(open(DUMP))
    yst = []
    for c in dump["countries"]:
        for r in c.get("regions", []):
            for s in r.get("settlements", []):
                for st in s.get("stations", []):
                    la, lo = st.get("latitude"), st.get("longitude")
                    try:
                        la, lo = float(la), float(lo)
                    except (TypeError, ValueError):
                        continue
                    if not la and not lo:
                        continue
                    yst.append({"title": st.get("title", ""), "settlement": s.get("title", ""),
                                "tt": st.get("transport_type", ""), "lat": la, "lon": lo})
    print(f"yandex stations with coords: {len(yst)}", file=sys.stderr)

    keep_stops = set()
    for s in d["schedules"]:
        if s["route"] in ROUTES:
            for st in s["stops"]:
                keep_stops.add(st["stop"])
    names = {st["id"]: st["name"] for st in d["stops"]}

    coords = {}
    for sid in sorted(keep_stops):
        name = names[sid]
        if sid in OVERRIDES:
            title, la, lo = OVERRIDES[sid]
            coords[sid] = {"title": title, "lat": la, "lon": lo}
            print(f"{sid} {name[:45]:45s} -> OVERRIDE {title} {la},{lo}")
            continue
        tw = set(tokens(name))
        best = None
        for y in yst:
            yw = set(tokens(y["title"]))
            inter = tw & yw
            if not inter:
                continue
            score = len(inter) / max(len(tw), len(yw))
            # бонус за совпадение населённого пункта
            if norm(y["settlement"]) and norm(y["settlement"]) in norm(name):
                score += 0.3
            if y["tt"] == "bus":
                score += 0.05
            if best is None or score > best[0]:
                best = (score, y)
        if best:
            coords[sid] = best[1]
            print(f"{sid} {name[:45]:45s} -> {best[0]:.2f} {best[1]['title'][:45]} [{best[1]['settlement']}] {best[1]['lat']},{best[1]['lon']}")
        else:
            print(f"{sid} {name[:45]:45s} -> NO MATCH")

    out = {
        "source": d.get("source", "mintrans"),
        "snapshot": "2026-05-10",
        "regions": d.get("regions", []),
        "routes": [r for r in d["routes"] if r.get("reg") in ROUTES],
        "stops": [],
        "services": [],
        "service_days": [],
        "service_exceptions": [],
        "carriers": [],
        "schedules": [],
    }
    for st in d["stops"]:
        if st["id"] not in keep_stops:
            continue
        row = dict(st)
        if st["id"] in coords:
            row["lat"] = coords[st["id"]]["lat"]
            row["lon"] = coords[st["id"]]["lon"]
        out["stops"].append(row)
    schedules = [s for s in d["schedules"] if s["route"] in ROUTES]
    out["schedules"] = schedules
    svc_ids = {s["service_id"] for s in schedules}
    out["services"] = [s for s in d.get("services", []) if s.get("id") in svc_ids]
    out["service_days"] = [sd for sd in d.get("service_days", []) if sd.get("service_id") in svc_ids]
    out["service_exceptions"] = [se for se in d.get("service_exceptions", []) if se.get("service_id") in svc_ids]
    want_carriers = {(r.get("carrier", ""), r.get("carrier_inn", "")) for r in out["routes"]}
    out["carriers"] = [c for c in d.get("carriers", [])
                       if (c.get("name", ""), c.get("inn", "")) in want_carriers]
    json.dump(out, open(OUT, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(f"wrote {OUT}: routes={len(out['routes'])} stops={len(out['stops'])} schedules={len(out['schedules'])}",
          file=sys.stderr)

main()
