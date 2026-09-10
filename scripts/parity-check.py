#!/usr/bin/env python3
"""Паритет store vs legacy intercity (Фаза 6, docs/14-plan.md §6).

Сравнивает JSON-срез реестра (regions.json) с канонической сетью в Postgres.
Паритет = store-сеть покрывает legacy-сеть на срезе регионов пилота:
у каждого маршрута реестра, трассирующего через регион пилота (стоп в
регионе), есть канонический маршрут mintrans (promoted или staged —
staging честная промежуточная станция конвейера, времена реально есть).

Флаг --all измеряет полный срез реестра (для дальних фаз — РФ целиком).

Запуск (dev):
  python3 scripts/parity-check.py --dsn "$DATABASE_DSN" --reestr regions.json \
    --pilot-region 42
"""
import argparse
import json
import sys


def norm(s):
    return " ".join(s.split()).strip().lower()


# Служебные слова реестра, которых нет в канонических именах терминалов:
# «АВ г. Белово» → терминал «Белово», «Остановочный пункт д. Марьевка» →
# «Марьевка». Для parity-сверки стоп-имя достаточно совпадения по
# значащим токенам (город/поселение), без служебной обвязки.
FILLER = {
    "ав", "ав.", "г", "г.", "с", "с.", "п", "п.", "пгт", "пгт.", "рп", "рп.",
    "д", "д.", "оп", "о", "о.", "ост", "остановочный", "пункт", "остановка",
    "автостанция", "ас", "автовокзал", "вокзал", "кассовый", "пункт»", "«кассовый",
}


def tokens(name):
    out = []
    for t in norm(name).replace("«", " ").replace("»", " ").replace("-", " ").split():
        t = t.strip(".,")
        if t and t not in FILLER and not t.isdigit():
            out.append(t)
    return out


def name_matches_canon(stop_name, canon_names):
    st = tokens(stop_name)
    if not st:
        return False
    for cn in canon_names:
        ct = set(tokens(cn))
        # первый значащий токен стопа (город/поселение) присутствует
        # в каноническом имени терминала
        if st[0] in ct:
            return True
    return False


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dsn", required=True)
    ap.add_argument("--reestr", required=True)
    ap.add_argument("--pilot-region", default="42",
                    help="регион пилота: маршрутом считается трасса со стопом в регионе (межрегиональные коды)")
    ap.add_argument("--all", action="store_true",
                    help="мерить полный срез реестра, не только регион пилота")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    with open(args.reestr) as f:
        ds = json.load(f)

    stopreg = {s["id"]: s.get("region", "") for s in ds["stops"]}
    timed = set()
    touching = set()
    for sc in ds["schedules"]:
        regs = {stopreg.get(st["stop"], "") for st in sc["stops"]}
        if not args.all and args.pilot_region not in regs:
            continue
        touching.add(sc["route"])
        for st in sc["stops"]:
            b = st.get("winter") or st.get("summer") or {}
            if b.get("dep") or b.get("arr"):
                timed.add(sc["route"])
                break

    import psycopg

    with psycopg.connect(args.dsn) as conn:
        store_routes = {
            row[0] for row in conn.execute(
                "SELECT external_route_code FROM routes WHERE source_provider='mintrans'"
            )
        }
        staged_routes = {
            row[0] for row in conn.execute(
                "SELECT DISTINCT external_route_code FROM staging_trips"
            )
        }
        store_stopnames = set()
        # Стопы реестра прикрепляются к терминалам (ось stop_terminal):
        # имя стопа реестра («Юргинский АВ») НЕ сохраняется как имя стопа —
        # канонический терминал носит имя OSM/Yandex. Покрытие стопов мерим
        # по терминалам, к которым прикреплены стопы promoted-минтранс-тридов.
        for row in conn.execute(
            """
            SELECT DISTINCT tn.name
            FROM stop_times st
            JOIN trips t ON t.id = st.trip_id
            JOIN stops_canonical sc ON sc.id = st.stop_id
            JOIN terminal_names tn ON tn.terminal_id = sc.terminal_id AND tn.lang = 'ru'
            WHERE t.provider_id = 'mintrans'
            """
        ):
            if row[0]:
                store_stopnames.add(norm(row[0]))

    reestr_routes = {r["reg"] for r in ds["routes"]}
    scope_routes = reestr_routes if args.all else touching & reestr_routes
    timed_scope = timed & scope_routes if not args.all else timed

    covered = store_routes | staged_routes
    matched_timed = timed_scope & covered
    missing_timed = sorted(timed_scope - covered)
    matched_routes = scope_routes & covered

    stops_scope = set()
    if not args.all:
        for sc in ds["schedules"]:
            if sc["route"] in scope_routes:
                for st in sc["stops"]:
                    if stopreg.get(st["stop"], "") == args.pilot_region:
                        stops_scope.add(st["stop"])
    stops_matched = 0
    stops_missing = []
    for sid in stops_scope:
        name = next((s["name"] for s in ds["stops"] if s["id"] == sid), "")
        if name_matches_canon(name, store_stopnames):
            stops_matched += 1
        else:
            stops_missing.append(name)

    scope = "all" if args.all else f"pilot={args.pilot_region}"
    print(f"scope: {scope}")
    print(f"reestr routes in scope: {len(scope_routes)}, timed: {len(timed_scope)}")
    print(f"covered (promoted {len(matched_routes & store_routes)} + staged: {len(matched_routes - store_routes)}): {len(matched_routes)}/{len(scope_routes)}")
    print(f"timed covered: {len(matched_timed)}/{len(timed_scope)}")
    if stops_scope:
        print(f"stops({args.pilot_region}) matched: {stops_matched}/{len(stops_scope)}")
    if missing_timed:
        print(f"missing timed: {missing_timed[:20]}")
    if stops_missing:
        print(f"missing stops sample: {stops_missing[:10]}")

    route_cov = len(matched_routes) / len(scope_routes) if scope_routes else 1.0
    timed_cov = len(matched_timed) / len(timed_scope) if timed_scope else 1.0
    ok = route_cov >= 0.95 and timed_cov >= 0.95
    print(f"parity: routes {route_cov:.1%}, timed {timed_cov:.1%} -> {'OK' if ok else 'FAIL'}")

    report = {
        "scope": scope,
        "reestr_routes_in_scope": len(scope_routes),
        "timed_in_scope": len(timed_scope),
        "covered_promoted": sorted(matched_routes & store_routes),
        "covered_staged": sorted(matched_routes - store_routes),
        "missing_timed": missing_timed,
        "missing_stops": stops_missing,
        "route_coverage": route_cov,
        "timed_coverage": timed_cov,
    }
    if args.out:
        with open(args.out, "w") as f:
            json.dump(report, f, ensure_ascii=False, indent=2)
        print(f"report: {args.out}")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
