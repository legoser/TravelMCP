# AGENTS.md

Контекст проекта для opencode-агента. Работаем только в этом репозитории.

## Что за проект

`travelmcp` — MCP-сервер на Go для мультимодального поиска маршрутов
общественного транспорта «дверь-в-дверь». Агент (или REST-клиент) передаёт
точки отправления/назначения и параметры поиска; сервер строит цепочку
перемещений (автобус/трамвай/поезд/самолёт + пешие подходы) через ядро
CSA-планировщика по канонической сети провайдеров данных.

Миссия — `docs/13-mission.md`, план и структура БД — `docs/14-plan.md`, MOTIS API — `docs/12-motis-api.md`.
Исторические `docs/01-description.md` / `docs/03-architecture.md` / `docs/05-roadmap.md` / `docs/07-first-provider-plan.md` — архив (см. `13`/`14`).

## Технологии и среда

- Go (модуль `travelmcp`, требования в `go.mod`; тулчейн по умолчанию).
- `github.com/mark3labs/mcp-go v0.58.0` — протокол MCP: Streamable HTTP,
  JSON-RPC 2.0, инструменты (`Tools`); транспорт работает stateless — без
  `initialize`/сессий (включается `WithStateLess`), вместо сессий —
  API-ключ в запросе.
- `gopkg.in/yaml.v3` — конфигурация.
- PostgreSQL + PostGIS — прод-стор (`docs/14-plan.md:3`), `PostGIS` для `places/terminals` гео.
- Внешние платные API не используются (только открытые источники). Продакшн-
  источников по умолчанию нет (пустой `PROVIDERS_ENABLED`); синтетический
  `synth` — мок-сеть **только** для тестов/демо, включается явно через
  `PROVIDERS_ENABLED=synth` или `NewRegistry([]string{"synth"})`.

## Структура (кратко)

```
cmd/mcp-server/        точка входа (конфиг → реестр → HTTP+MCP → shutdown)
internal/model/        каноническая модель (ядро, без внешних зависимостей)
internal/providers/    Provider-интерфейс, Registry, synth (мок для тестов/демо),
                       intercity (реальный источник: реестр Минтранса, exact-времена)
internal/geo/          гаверсин, ближайшие остановки, время пешего доступа
internal/planner/      CSA-поиск + сборка Journey
internal/mcp/          MCP-инструменты: find_route, list_providers
internal/server/       http-роутер: /healthz, /readyz, /api/v1/*, монтаж /mcp
internal/telemetry/    счётчики метрик
internal/config/       конфиг: default → YAML → env
test/common/           заготовленные сценарии и хелперы (integration + smoke)
test/integration/      тесты HTTP+MCP in-process (httptest)
test/smoke/            тесты с реальным бинарником (отдельный процесс)
testdata/              эталонные фикстуры провайдеров (synth, intercity/mini.json) — коммитятся
tools/osm-extract/     отдельный Go-модуль: PBF (OSM) → JSON гео-меток
data/                  сырьё и датасеты сбора intercity (НЕ коммитятся)
scripts/api-demo.sh    ручное демо/обследование API (curl+jq)
scripts/mcp-route.sh   шаблоны запросов MCP: find_route (пункт/координаты), tools, providers
scripts/yandex-collect.sh  точечный сбор фикстур Яндекса (env-ключи)
scripts/extract-minstran.py  XLSX-реестр Минтранса → JSON датасет
configs/               YAML-конфиги
```

## Правила работы

- Не добавляй комментарии в код, если пользователь явно не попросил.
- Рассуждения выполняй на английском языке, а ответы давай на русском.
- Контролируй зацикливание действий, лучше спросить пользователя в затруднительной ситуации.
- Каноническая модель живёт только в `internal/model`; адаптеры приводят к ней
  данные, а не дублируют типы в своих пакетах.
- Направление зависимостей «сверху вниз»: `cmd` → `server` → `mcp`/`planner` →
  `providers`/`geo` → `model`. Никто не импортирует `server`/`mcp`/`cmd` наверх.
  Внешние вызовы — только через адаптеры и единый HTTP-слой.
- Идентификаторы и коды — латиницей (`a-cen`, `900`, `ModeFlight`).
  Человекочитаемые строки (названия остановок, тексты ошибок, описания
  инструментов) — по-русски; это пользовательская конвенция проекта.
- Точные изменения в конфигурации: default → YAML → env-переопределение
  (`HTTP_ADDR`, `DATABASE_DSN`, `PROVIDERS_ENABLED`, `INTERCITY_REESTR_PATH`,
  `ADMIN_TOKEN`). Секреты — только через env, в репозиторий не попадают.
- Модель данных стабильна; внутренние реализации меняются свободно.
- Программные `Makefile`-цели — основной способ сборки/тестов; Go-тесты —
  основной способ проверки API, shell-скрипт — только ручное демо.
- Тест-сценарии не генерировать на лету: фиксированные заготовки в
  `test/common` одинаково исполняются и в integration, и в smoke.
- Если добавляешь новое поле/инструмент MCP — дополни автоматизированные
  сценарии в `test/common` и проверь `scripts/api-demo.sh`.
- После изменения конфигурации opencode (включая этот файл) — напомни
  пользователю перезапустить opencode: конфиг читается при старте.
- значения определяющие параметры поведения кода и допустимые к изменению, должны быть в едином месте для конфигурирования.

## Команды

```sh
make build          # go build -o bin/mcp-server ./cmd/mcp-server
make test           # точка входа в тестирование: unit + integration + smoke
make unit           # go test ./internal/... -count=1
make integration    # go test ./test/integration/ -count=1 -v
make smoke          # go test ./test/smoke/ -count=1 -v
make run            # go run ./cmd/mcp-server -config configs/config.example.yaml
                    #   (источников нет; для демо с мок-сетью: PROVIDERS_ENABLED=synth make run;
                    #    для реального реестра: PROVIDERS_ENABLED=intercity make run —
                    #    нужен датасет data/reestr/regions.json см. extract-minstran.py)
make vet            # go vet ./...
make fmt            # gofmt -w .
./scripts/api-demo.sh   # ручное демо API/MCP (мок synth по PROVIDERS_ENABLED=synth, требует jq)
./scripts/mcp-route.sh  # шаблоны MCP-запросов (см. usage): place/coords/tools/providers;
                        #   env: BASE, TOKEN; пример: ./scripts/mcp-route.sh place Юрга Барнаул
./scripts/yandex-collect.sh  # сбор фикстур Яндекса (требует env-ключи, jq)
python3 scripts/extract-minstran.py --in data/raw/minstran/reestr.xlsx \
  --regions 22,42,54,70 --snapshot 2026-06-16 \
  --osm data/osm/stations.json -o data/reestr/regions.json  # XLSX → JSON + геокодинг
(cd tools/osm-extract && go run . -in ../../data/raw/osm/sfo.osm.pbf \
  -out ../../data/osm/stations.json)  # PBF → JSON гео-меток
```

Обязательная проверка после изменений если это программный код, аначе пропустить: `gofmt` + `go vet ./...` + `make test`.

## Референс мок-данных (synth, только для тестов/демо)

- Кластеры: A «Пермь» (`a-cen`, `a-bus`, `a-air`), B «Екатеринбург» (`b-bus`,
  `b-mkt`, `b-apt`), C «ПГУ» (`c1`, `c2`, `c2x`, `c3`), аэропорт `a-apt`.
- Виды транспорта: `walk`, `bus`, `tram`, `rail`... , `flight`. Регулярки:
  автобус «a» (10 мин), межгород «900» (60 мин), трамвай «b» (6 мин),
  «c»/«d» без избыточности... , шаттлы «s1»/«s2» (30 мин), перелёт «fly»
  (120 мин).
- Пешие стыковки: `b-bus ↔ b-mkt` (6 мин), `c2 ↔ c2x` (5 мин).
- Стабильные ожидания тестов: Пермь→Екб наземкой (06:00) — прибытие
  `09:36`, 5 legов, 2 пересадки; перелёт аэропорт→аэропорт (07:00) —
  прибытие `09:05`, 0 пересадок; кластер C→B — ошибка «маршрут не найден».
- Порог пешей доступности по умолчанию — 30 мин (5 км/ч).

## Референс данных intercity (реестр Минтранса)

- Источник exact-времён: `scripts/extract-minstran.py` (XLSX → JSON) с опц.
  `--osm data/osm/stations.json` для геокодинга остановок (140/255 с
  координатами). Рабочий датасет `data/reestr/regions.json` (НЕ коммитится);
  фикстура для тестов `testdata/reestr/mini.json` — коммитится.
- Проверенный сквозной сценарий (поиск 06:00 UTC, limit walk 30 мин):
  НСК-автовокзал → Барнаул/Томск/Кемерово находятся и строятся CSA.
- Пешие стыковки между близкими терминалами (< 0,4 км) строятся
  автоматически (`addTransferLinks`), т.к. соседние вокзал/автостанция —
  разные стопы реестра.
- Сеть лентяйно строится на каждый запрос: `intercity` читает JSON при каждом
  `Network()` и в `Health()` — кэш/TTL ещё не добавлены.

## Документация

- `docs/13-mission.md` — миссия (полноценная БД, один `gtfs.zip`).
- `docs/14-plan.md` — план, структура БД и связи.
- `docs/12-motis-api.md` — MOTIS API (`v6` + кириллица `urlencode`).
- `docs/02-glossary.md` — термины.
- `docs/01/03/05/06/07` — архив до `13/14`.