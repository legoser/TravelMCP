#!/usr/bin/env python3
"""Привязка остановок среза реестра к координатам офлайн-дампа Яндекса.

Производный артефакт (сырьё не трогаем): на вход — срез без координат,
на выход — тот же срез с lat/lon у уверенно сматченных остановок.
Несматченные остаются без координат и честно уходят в staging (§5.4),
а не получают угаданные точки.

  python3 scripts/match-reestr-yandex.py data/scratch/reestr42.json \\
      data/yandex/cache/global_stations_list.json data/scratch/reestr42.geo.json

Матчинг: токены названия + бонус за населённый пункт (как в
make-test-fixture.py) + sanity-порог. Ручные привязки хабов — OVERRIDES.
"""

import json
import re
import sys
import unicodedata

IN = sys.argv[1] if len(sys.argv) > 1 else "data/scratch/reestr42.json"
DUMP = sys.argv[2] if len(sys.argv) > 2 else "data/yandex/cache/global_stations_list.json"
OUT = sys.argv[3] if len(sys.argv) > 3 else "data/scratch/reestr42.geo.json"
MIN_SCORE = 0.5


def norm(s):
    s = unicodedata.normalize("NFKC", s or "").lower().replace("ё", "е")
    s = re.sub(r"[«»\"'().,/-]", " ", s)
    s = re.sub(r"\s+", " ", s).strip()
    return s


STOPWORDS = {"оп", "ав", "ас", "дкп", "вокзал", "главный", "г", "рп", "с", "ао",
             "остановочный", "пункт", "станция", "платформа", "касса", "автостанция",
             "автовокзал", "остановка", "поворот", "дк", "кп"}


def tokens(s):
    return [t for t in norm(s).split() if t not in STOPWORDS and len(t) > 2]


OVERRIDES = {
    "op:54:54106": ("Новосибирск, автостанция Речной вокзал", 55.008070590379944, 82.93647936902936),
    "op:54:54107": ("Новосибирск, автостанция Речной вокзал", 55.008070590379944, 82.93647936902936),
    "op:70:70016": ("Томск, автовокзал", 56.4612664816162, 84.9912958591767),
    "op:54:54018": ("Аэропорт Толмачёво, автобус", 55.00821345943963, 82.66753068061814),
}


def main():
    d = json.load(open(IN))
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
                                "region": r.get("title", ""), "tt": st.get("transport_type", ""),
                                "lat": la, "lon": lo})
    print(f"yandex stations with coords: {len(yst)}", file=sys.stderr)

    matched, override, unsure = 0, 0, []
    for st in d["stops"]:
        sid, name = st["id"], st["name"]
        if sid in OVERRIDES:
            title, la, lo = OVERRIDES[sid]
            st["lat"], st["lon"] = la, lo
            override += 1
            print(f"{sid} {name[:45]:45s} -> OVERRIDE {title}")
            continue
        tw = set(tokens(name))
        if not tw:
            unsure.append((sid, name, "no-tokens", 0.0, ""))
            continue
        best = None
        for y in yst:
            yw = set(tokens(y["title"]))
            inter = tw & yw
            if not inter:
                continue
            score = len(inter) / max(len(tw), len(yw))
            settled = bool(norm(y["settlement"]) and norm(y["settlement"]) in norm(name))
            if settled:
                score += 0.3
            if y["tt"] == "bus":
                score += 0.05
            if best is None or score > best[0]:
                best = (score, y, settled)
        if best and best[0] >= MIN_SCORE and (best[2] or best[0] >= 0.8):
            st["lat"], st["lon"] = best[1]["lat"], best[1]["lon"]
            matched += 1
            print(f"{sid} {name[:45]:45s} -> {best[0]:.2f} {best[1]['title'][:40]} [{best[1]['settlement']}]")
        else:
            info = f"{best[0]:.2f} {best[1]['title'][:40]} [{best[1]['settlement']}]" if best else "NO MATCH"
            unsure.append((sid, name, info, best[0] if best else 0.0, ""))
    json.dump(d, open(OUT, "w"), ensure_ascii=False, indent=1)
    print(f"matched={matched} override={override} unsure={len(unsure)} total={len(d['stops'])} -> {OUT}", file=sys.stderr)
    for sid, name, info, _, _ in unsure:
        print(f"UNSURE {sid} {name[:50]:50s} {info}")


main()
