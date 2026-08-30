# AGENTS.md

Контекст проекта для opencode-агента. Работаем только в этом репозитории.

## Что за проект

`travelmcp` — MCP-сервер на Go для мультимодального поиска маршрутов
общественного транспорта «дверь-в-дверь». Агент (или REST-клиент) передаёт
точки отправления/назначения и параметры поиска; сервер строит цепочку
перемещений (автобус/трамвай/поезд/самолёт + пешие подходы) через ядро
CSA-планировщика по канонической сети провайдеров данных.

Полное описание — `docs/01-description.md`, целевая архитектура —
`docs/03-architecture.md`, фактическая структура — `docs/06-structure.md`.

## Технологии и среда

- Go (модуль `travelmcp`, требования в `go.mod`; тулчейн по умолчанию).
- `github.com/mark3labs/mcp-go v0.58.0` — протокол MCP: Streamable HTTP,
  JSON-RPC 2.0, инструменты (`Tools`), неявные сессии по заголовку
  `Mcp-Session-Id`.
- `gopkg.in/yaml.v3` — конфигурация.
- PostgreSQL + PostGIS — зарезервированы (`docker-compose.yml`), в коде
  каркаса **не используются**.
- Внешние платные API не используются (только открытые источники). На старте
  единственный провайдер данных — синтетический `synth`.

## Структура (кратко)

```
cmd/mcp-server/        точка входа (конфиг → реестр → HTTP+MCP → shutdown)
internal/model/        каноническая модель (ядро, без внешних зависимостей)
internal/providers/    Provider-интерфейс, Registry, synth (данные-фикстура)
internal/geo/          гаверсин, ближайшие остановки, время пешего доступа
internal/planner/      CSA-поиск + сборка Journey
internal/mcp/          MCP-инструменты: find_route, list_providers
internal/server/       http-роутер: /healthz, /readyz, /api/v1/*, монтаж /mcp
internal/telemetry/    счётчики метрик
internal/config/       конфиг: default → YAML → env
test/common/           заготовленные сценарии и хелперы (integration + smoke)
test/integration/      тесты HTTP+MCP in-process (httptest)
test/smoke/            тесты с реальным бинарником (отдельный процесс)
testdata/              эталонные фикстуры провайдера intercity (коммитятся)
tools/osm-extract/     отдельный Go-модуль: PBF (OSM) → JSON гео-меток
data/                  сырьё и датасеты сбора intercity (НЕ коммитятся)
scripts/api-demo.sh    ручное демо/обследование API (curl+jq)
scripts/yandex-collect.sh  точечный сбор фикстур Яндекса (env-ключи)
scripts/extract-minstran.py  XLSX-реестр Минтранса → JSON датасет
configs/               YAML-конфиги
```

## Правила работы

- Не добавляй комментарии в код, если пользователь явно не попросил.
- Каноническая модель живёт только в `internal/model`; адаптеры приводят к ней
  данные, а не дублируют типы в своих пакетах.
- Направление зависимостей «сверху вниз»: `cmd` → `server` → `mcp`/`planner` →
  `providers`/`geo` → `model`. Никто не импортирует `server`/`mcp`/`cmd` наверх.
  Внешние вызовы — только через адаптеры и единый HTTP-слой.
- Идентификаторы и коды — латиницей (`a-cen`, `900`, `ModeFlight`).
  Человекочитаемые строки (названия остановок, тексты ошибок, описания
  инструментов) — по-русски; это пользовательская конвенция проекта.
- Точные изменения в конфигурации: default → YAML → env-переопределение
  (`HTTP_ADDR`, `DATABASE_DSN`, `PROVIDERS_ENABLED`, `ADMIN_TOKEN`). Секреты —
  только через env, в репозиторий не попадают.
- Модель данных стабильна; внутренние реализации меняются свободно.
- Программные `Makefile`-цели — основной способ сборки/тестов; Go-тесты —
  основной способ проверки API, shell-скрипт — только ручное демо.
- Тест-сценарии не генерировать на лету: фиксированные заготовки в
  `test/common` одинаково исполняются и в integration, и в smoke.
- Если добавляешь новое поле/инструмент MCP — дополни автоматизированные
  сценарии в `test/common` и проверь `scripts/api-demo.sh`.
- После изменения конфигурации opencode (включая этот файл) — напомни
  пользователю перезапустить opencode: конфиг читается при старте.

## Команды

```sh
make build          # go build -o bin/mcp-server ./cmd/mcp-server
make test           # точка входа в тестирование: unit + integration + smoke
make unit           # go test ./internal/... -count=1
make integration    # go test ./test/integration/ -count=1 -v
make smoke          # go test ./test/smoke/ -count=1 -v
make run            # go run ./cmd/mcp-server -config configs/config.example.yaml
make vet            # go vet ./...
make fmt            # gofmt -w .
./scripts/api-demo.sh   # ручное демо API/MCP (требует jq)
./scripts/yandex-collect.sh  # сбор фикстур Яндекса (требует env-ключи, jq)
(cd tools/osm-extract && go run . -in ../../data/raw/osm/sfo.osm.pbf \
  -out ../../data/osm/stations.json)  # PBF → JSON гео-меток
```

Обязательная проверка после изменений: `gofmt` + `go vet ./...` + `make test`.

## Референс данных (synth-сеть)

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

## Документация

- `docs/01-description.md` — цель, сценарии, принципы, границы.
- `docs/02-glossary.md` — термины и технологии.
- `docs/03-architecture.md` — целевая компонентная архитектура.
- `docs/04-data-sources.md` — источники данных.
- `docs/05-roadmap.md` — дорожная карта.
- `docs/06-structure.md` — структура репозитория и связи (актуальный каркас).
- `docs/07-first-provider-plan.md` — рабочий план провайдера междугородних автобусов (этапы 0–3).