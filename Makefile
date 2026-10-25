include .env
export

.PHONY: build verify-isolation db-up db-down db-logs migrate-up migrate-down migrate-create migrate-version migrate-force run run-api-only seed-admin test test-db test-integration swagger fmt vet

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/movies-api ./cmd/api

verify-isolation: build
	./tools/verify-isolation.sh

db-up:
	docker compose up -d

db-down:
	docker compose down

db-logs:
	docker compose logs -f db

migrate-up:
	@docker compose run --rm migrate -path=/migrations -database "$$DATABASE_URL" up

migrate-down:
	@docker compose run --rm migrate -path=/migrations -database "$$DATABASE_URL" down 1

migrate-create:
	@test -n "$(name)" || (echo "Usage: make migrate-create name=add_movies" >&2; exit 1)
	docker compose run --rm --entrypoint migrate migrate create -ext sql -dir /migrations -seq "$(name)"

migrate-version:
	@docker compose run --rm migrate -path=/migrations -database "$$DATABASE_URL" version

migrate-force:
	@test -n "$(version)" || (echo "Usage: make migrate-force version=1" >&2; exit 1)
	@docker compose run --rm migrate -path=/migrations -database "$$DATABASE_URL" force "$(version)"

run:
	DB_HOST=127.0.0.1 WEB_UI_ENABLED=true go run ./cmd/api

run-api-only:
	DB_HOST=127.0.0.1 WEB_UI_ENABLED=false go run ./cmd/api

seed-admin:
	DB_HOST=127.0.0.1 go run ./cmd/seed

test:
	env -u TEST_DATABASE_URL go test ./...

test-db:
	@case "$$TEST_DB_NAME" in *_test) ;; *) echo "TEST_DB_NAME must end in _test" >&2; exit 1;; esac
	@test "$$TEST_DB_NAME" != "$$DB_NAME" || (echo "TEST_DB_NAME must differ from DB_NAME" >&2; exit 1)
	@docker compose up -d --wait db
	@docker compose exec -T db sh -c 'if psql -U "$$POSTGRES_USER" -d postgres -lqt | cut -d "|" -f 1 | grep -qw "$$TEST_DB_NAME"; then echo "Test database exists"; else createdb -U "$$POSTGRES_USER" "$$TEST_DB_NAME"; fi'
	@docker compose run --rm migrate -path=/migrations -database "$$TEST_DATABASE_DOCKER_URL" up

test-integration: test-db
	@go test -race ./internal/repository -run '^(TestGormRepositoryIntegration|TestGormUserRepositoryIntegration|TestGormMovieSearchIntegration)$$' -v

swagger:
	go run github.com/swaggo/swag/cmd/swag@v1.16.4 init -g main.go -d cmd/api,internal/handler,internal/model

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.git/*')

vet:
	go vet ./...
