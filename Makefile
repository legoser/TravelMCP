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