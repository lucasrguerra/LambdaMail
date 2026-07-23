.PHONY: up down logs test lint migrate-up migrate-down gen-dev-cert seed

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

gen-dev-cert:
	mkdir -p local/certs
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
		-keyout local/certs/key.pem -out local/certs/cert.pem \
		-days 365 -nodes -subj "/CN=mail.localhost"

seed:
	docker compose exec -T db psql -U $${POSTGRES_USER:-lambdamail} -d $${POSTGRES_DB:-lambdamail} < scripts/dev-seed.sql
