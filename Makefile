.PHONY: up down logs test lint migrate-up migrate-down

up:
	docker compose --env-file .env up --build

down:
	docker compose down -v

logs:
	docker compose logs -f

test:
	cd services/protocols && go test ./...
	cd services/auth && npm test
	cd services/storage && npm test

lint:
	cd services/protocols && go vet ./...
	node scripts/lang-lint.mjs

migrate-up:
	docker run --rm -v $(PWD)/migrations:/migrations --network host \
		migrate/migrate -path=/migrations -database "$$DATABASE_URL" up

migrate-down:
	docker run --rm -v $(PWD)/migrations:/migrations --network host \
		migrate/migrate -path=/migrations -database "$$DATABASE_URL" down 1
