.PHONY: test test-unit test-integration build up down

test: test-unit test-integration

test-unit:
	go test ./internal/domain/... ./internal/dto/... ./internal/utils/... ./internal/adapter/driven/modelclient/... ./internal/adapter/driven/pdf/... ./internal/adapter/driver/http/... ./internal/adapter/driver/multipartform/... ./internal/adapter/driver/web/... ./internal/adapter/driver/kafka/... ./internal/service/...

test-integration:
	go test -tags=integration ./internal/adapter/driven/postgres/... ./internal/adapter/driven/kafka/... ./internal/adapter/driver/kafka/...

build:
	go build ./...

up:
	docker compose up --build

down:
	docker compose down -v
