BIN := bin/mcp-server

.PHONY: build test unit integration smoke vet fmt run clean

## build — собрать бинарник сервера
build:
	go build -o $(BIN) ./cmd/mcp-server

## test — точка входа в тестирование: unit + integration + smoke
test:
	go test ./... -count=1

## unit — только модульные тесты внутренних пакетов
unit:
	go test ./internal/... -count=1

## integration — HTTP+MCP-обвязка in-process (без запуска процесса)
integration:
	go test ./test/integration/ -count=1 -v

## smoke — сборка бинарника и запуск отдельного процесса
smoke:
	go test ./test/smoke/ -count=1 -v

vet:
	go vet ./...

fmt:
	gofmt -w .

run:
	go run ./cmd/mcp-server -config configs/config.dev.yaml

run-example:
	go run ./cmd/mcp-server -config configs/config.example.yaml

clean:
	rm -rf $(BIN) bin

.PHONY: check-layers check-deprecated

## check-layers: проверка направления зависимостей и SOLID-границ (AGENTS.md)
check-layers:
	@echo "==> go vet"
	go vet ./...
	@echo "==> store не должен импортировать providers/server/mcp"
	@if grep -rl "travelmcp/internal/providers\|travelmcp/internal/server\|travelmcp/internal/mcp" internal/store --include="*.go" | grep -v "_test.go"; then \
		echo "FAIL: internal/store импортирует верхние слои"; exit 1; \
	fi
	@echo "==> ни один SQLiteStore не раскидан по нескольким файлам без явного разделения по ответственности"
	@grep -rl "SQLiteStore" internal/store --include="*.go" | grep -v "_test.go" | sort
	@echo "OK (просмотреть список выше вручную — не все совпадения являются нарушением)"

## check-deprecated: поиск устаревшего кода после закрытия пункта плана
## (см. docs/15-dev-status.md §6 за объяснением каждого паттерна)
check-deprecated:
	@echo "==> явно помеченный устаревший код"
	@grep -rn "Deprecated\|TODO.*phase" --include="*.go" . || true
	@echo "==> файлы, привязанные к legacy JSON/sqlite-пути"
	@grep -rln "seed_pilot\|places_sqlite\|import_intercity" --include="*.go" . || true
	@echo "==> напоминание: сверить найденное со статусом перехода в docs/15-dev-status.md §2"