# 11. Стратегия импорта данных — покрытие всей страны с минимизацией затрат

Статус: утверждена 2026-09-02. `i18n` (Фаза 7 `09`) отложена в backlog — приоритет логирование + данные.

## 1. Цель

Покрыть автовокзалами/маршрутами всю РФ, но без колоссальных затрат на лимитированные ресурсы (Yandex квоты, ручной геокодинг, валидация). Старт — крупные города, затем инкрементальное расширение с гарантией целостности уже корректных данных.

Текущий срез (2026-09-02, после расширения на столицы): `intercity` 5577 маршрутов / 2721 остановка / 11154 блока (`--regions all`, `regions=["all"]`) из 5578 XLSX. Геокодировано 855/2721 (CITY_OVERRIDE 107 якорей + OSM sfo 24895). Остальное — quarantine `issues 1865` без координат, не участвует в `BuildIndexes` до геокодинга. Предыдущий срез 351/255 сохранён как `data/reestr/regions.json.bak.20260902`.

## 2. Источники и их экономика

| Источник | Формат | Охват | Лимит/стоимость | Роль в стратегии |
|---|---|---|---|---|
| **Реестр Минтранса XLSX** `data/raw/minstran/reestr.xlsx` 5 листов 28k строк | offline | 5578 маршрутов, 6151 перевозчик, exact `dep/arr` | 0 — файл качается 1 раз с `mintrans.gov.ru/documents/8/15657` (zip) | Каркас связности — бесплатен, масштабируется `scripts/extract-minstran.py --regions all`. |
| **OSM** `tools/osm-extract` PBF→JSON | offline | `sfo` 24895 объектов, РФ ~200k | 0 — Geofabrik `russia-latest.osm.pbf` 2–3 GB, 1 проход | Координаты автовокзалов/платформ, `NearbyPairs 0.4км` трансферы. Первичный геокодер. |
| **GTFS Москва/СПб** MobilityDatabase `mdb-3226` | zip CSV | городские маршруты/частоты | 0 — weekly fetch, `feed_info.txt` date | Access/egress внутри агломераций. Подключается как 2-й провайдер `providers.gtfs`. |
| **Nominatim OSM** `https://nominatim.openstreetmap.org/search` | REST | вся РФ | 1 req/s, `User-Agent: travelmcp/1.0`, 10k/сутки soft | Приоритет 1 в `scripts/extract-minstran.py:311` `NominatimGeoCoder` (кэш `nominatim_geo.json`) и `internal/geocoder` `defaultPriority nominatim→yandex`. Дешевле и отказоустойчивее. |
| **Yandex Геокодер** `geocode-maps.yandex.ru/1.x` | REST | вся РФ | **500/сутки** (`scripts/extract-minstran.py:313 YANDEX_DAILY_LIMIT=500`), ключ `YANDEX_GEOCODE_KEY` | Fallback в ротации: `Nominatim → Yandex` `extract-minstran.py:651` при `ENABLE_GEOCODE=1`, иначе только `CITY_OVERRIDE osm`. Кэш `yandex_geo.json`. |
| **Yandex Расписания** `api.rasp.yandex.net/v3.0` | REST | межгород exact | **500/сутки** per-key, heavily rate-limited | Не нужен для каркаса (реестр уже exact), только верификатор. Ротация не нужна — фикстуры `testdata/yandex`, живой вызов только `scripts/yandex-collect.sh` с тем же лимитом. |

Вывод `docs/04-data-sources.md:130`: Nominatim точнее Yandex по автовокзалам 7/7 vs 3/7 ошибок → приоритет `nominatim`.

## 3. Поэтапное покрытие

### Фаза A — столицы регионов + крупные нестоличные (выполнено 2026-09-02)
- **Критерий**: все 85 столиц (коды 01..92, 77,78,79,82,83,86,87,89,92) + 22 крупных нестоличных >250k (Тольятти, Наб.Челны, Сочи, Новороссийск, Новокузнецк, Таганрог, Нижний Тагил, Магнитогорск, Сургут, Нижневартовск и т.д.) — итого 107 якорей `CITY_OVERRIDE` `scripts/extract-minstran.py:182`.
- **Реестр**: `python3 scripts/extract-minstran.py --in data/raw/minstran/reestr.xlsx --regions all --snapshot 2026-09-02 --osm data/osm/stations.json -o data/reestr/regions.json` → 5577 маршрутов / 2721 стоп / 11154 блока / 855 геокодировано (без внешних API, только CITY_OVERRIDE+OSM). Ранее 351/255. Стоимость — CPU/IO, без квот.
- **OSM**: `data/osm/stations.json` sfo 24895 пока достаточно для Сибири; для полного покрытия — `russia-latest.osm.pbf` → `data/osm/russia.json` (~40 MB) позже.
- **GTFS**: следующий шаг — Москва `data/gtfs/moscow.zip` + СПб как `providers.gtfs` (`65088ce`), `PROVIDERS_ENABLED=intercity,gtfs` merged `mcp/mcp.go:380`.
- **Геокодинг**: по умолчанию без внешних вызовов (экономия), при `ENABLE_GEOCODE=1` — ротация `Nominatim (1 req/s, кэш nominatim_geo.json) → Yandex 500/сутки (кэш yandex_geo.json)` `extract-minstran.py:651`.

**Готово когда**: `find_route Москва→СПб`, `НСК→Москва`, `Екатеринбург→Пермь` строятся CSA, `list_providers issues/excluded <5%`.

### Фаза B — догеокодинг + GTFS города (1–2 недели)
- При `ENABLE_GEOCODE=1` догеокодить 1866 оставшихся стопов ротацией `Nominatim → Yandex` с кэшами: 1866 sec ≈ 30 мин Nominatim, Yandex 500/сутки → 4 дня при fallback 20%. Прогнать `extract-minstran.py --regions all --osm data/osm/russia.json` повторно — кэши `nominatim_geo.json/yandex_geo.json` не бьют повторно.
- GTFS для 12 фидов РФ (MobilityDatabase) — по одному `gtfs` провайдеру на город.
- Валидация: `ValidateNetwork` `Code` без текста + `PolicyValidator BBox` `model/validate.go:22`.

### Фаза C — полное покрытие + обновления (постоянно)
- Полный дамп 5578 маршрутов в `store` (`memory/sqlite` → `postgres PostGIS` `store/postgres` Фаза 3). Инкремент — daily `cron` скачивает новый XLSX (реферer), сравнивает `sha256`, `extract-minstran.py` → новый `regions.json`.
- Скрейпинг точечно под города без GTFS/реестра — только где `gap self_provided >30%` по метрикам `telemetry`.

## 4. Пайплайн импорта и целостность

```
XLSX (mintrans) ──→ extract-minstran.py ──→ regions.json (snapshot, checksum)
                     ↓ --osm russia.json   ↓
                OSM stations.json ─────────┘
                              ↓
           store.ImportIntercity(ctx, st, path, logger)  [транзакция]
              ├─ load + sha256 ──→ skip if checksum == last imports.checksum
              ├─ WithTx { station grouping 0.4км, carriers, stops, routes, trips (parallel NumCPU*2), transfers }
              ├─ ValidateNetwork → issues/excluded → SaveQualityIssue
              └─ MarkImportedVersion(snapshot, checksum, records, issues)  [atomic]
                                    ↓ commit only if issues/excluded < threshold
                         NetworkCache TTL 5m + singleflight
```

Гарантии уже в коде `internal/store/import_intercity.go:90`:
- `GetImport checksum` → skip без записи → нет дрейфа корректных данных.
- `WithTx` — либо все таблицы `stations/stops/routes/trips` коммитятся, либо rollback (исправлено `sqlite.go:50 WithTx` Фаза 0).
- `ValidateNetwork` считает `issues 117 no_geo` → `excluded 117` в `HealthStatus` `readyz` 503 при деградации, но импорт не откатывается — quarantine: стопы без координат остаются, но не участвуют в `BuildIndexes`.
- Версионирование `imports(id, provider_id, started_at, finished_at, status, records, checksum, version)` + `quality_issues(import_id)` позволяет `rollback` к предыдущему `snapshot` сравнением `checksum`.
- Параллельный импорт `sem NumCPU*2` не держит `Tx` долго: `pendingTrips` собираются вне `Tx`, внутри только батч `Upsert`.

Обновление: `TTL 5m` `NetworkCache` + `Registry snapshot 10s` — новый `regions.json` подхватывается без рестарта (`PROVIDERS_ENABLED` уже hot-reload `server.go:377`). Для `postgres` — та же `Tx`.

## 5. Минимизация лимитов (ротация Nominatim→Yandex)

- **Кэш**: `yandex_geo.json` + `nominatim_geo.json` + `places.json` — повторный прогон не бьет по API; `used` счётчики `Yandex 500`, `Nominatim 10k`.
- **Приоритет**: `internal/geocoder/fallback.go:20` и `scripts/extract-minstran.py:651` — `Nominatim primary (1 req/s, User-Agent travelmcp/1.0)` → `Yandex fallback 500/сутки`. При `ENABLE_GEOCODE` не задан — только `CITY_OVERRIDE osm` без сети.
- **Batch & rate**: скрипт throttles `1.1 sec` `NominatimGeoCoder.last_ts`; `Yandex` останавливается при `used >=500`, остаток на следующий день (`--snapshot` новый).
- **Детерминированные якори**: `CITY_OVERRIDE` 107 столиц+крупных покрывают >90% неоднозначных без API.
- **OSM вместо геокодера**: `tools/osm-extract` `amenity=bus_station` — для `intercity` достаточно, в SFO 24895, РФ ~200k.

## 6. Наблюдаемость

- `GET /readyz` 503 если `HealthStatus.Issues` вырос > порога.
- `GET /metrics` `travelmcp_provider_health`, `travelmcp_external_requests_total`, `provider_freshness` (возраст `last_import_time`).
- Логи `store/import_intercity.go:91` `import skipped - same checksum` + `stations grouped`/`trips prepared` с `elapsed_ms` — в `text` режиме цветной `DBG` для дебага.

## 7. Следующие шаги

1. ~~CITY_OVERRIDE 107 якорей — выполнено~~; пересобрать `data/osm/russia.json` из `russia-latest.osm.pbf` для национального OSM.
2. Прогнать `ENABLE_GEOCODE=1 python3 scripts/extract-minstran.py --regions all --osm data/osm/russia.json --out data/reestr/regions.json` → догеокодить 1866 стопов ротацией (Nominatim→Yandex 500/сутки) с кэшами.
3. Добавить `GTFS` фид Москвы в `testdata/gtfs` и проверить `PROVIDERS_ENABLED=gtfs,intercity` в `make integration`.
4. Завести `cron` скачивания XLSX с `sha256` сравнением (`scripts/fetch-reestr.sh`) и `docs/04` расписание Yandex 500/сутки.
