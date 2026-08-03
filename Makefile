.PHONY: up down logs test lint migrate-up migrate-down gen-dev-cert seed preflight create-admin reset-password reset-mfa

# Local development builds from source; production pulls what CI published.
up:
	docker compose -f docker-compose.yaml -f docker-compose.build.yaml --env-file .env up --build

down:
	docker compose down -v

logs:
	docker compose logs -f

test:
	cd services/protocols && go test ./...
	cd services/auth && npm test
	cd services/storage && npm test
	cd services/webmail && npm test

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

# Environment diagnostics (PLAN.md section 15). Run this before pointing DNS at
# the host: it reports outbound port 25, PTR/FCrDNS, RBL listings and the
# Cloudflare token scope.
preflight:
	docker compose run --rm --entrypoint /app/lambdamail-protocols protocols preflight

# First login on a fresh deployment. Nothing else creates a mailbox, so without
# this there is no way into the admin console.
#   make create-admin EMAIL=you@example.com PASSWORD='...'
create-admin:
	@test -n "$(EMAIL)" || (echo "EMAIL is required" && exit 1)
	@test -n "$(PASSWORD)" || (echo "PASSWORD is required" && exit 1)
	docker compose run --rm --entrypoint node auth dist/cli.js create-admin "$(EMAIL)" "$(PASSWORD)"

# Recovers a locked-out account. Every session it had is signed out.
reset-password:
	@test -n "$(EMAIL)" || (echo "EMAIL is required" && exit 1)
	@test -n "$(PASSWORD)" || (echo "PASSWORD is required" && exit 1)
	docker compose run --rm --entrypoint node auth dist/cli.js reset-password "$(EMAIL)" "$(PASSWORD)"

# Clears every second factor from an account: the lost-phone case, and the one
# where an enrollment was confirmed against a secret the authenticator does not
# have. The password is untouched, so this alone grants nobody access.
#   make reset-mfa EMAIL=you@example.com
reset-mfa:
	@test -n "$(EMAIL)" || (echo "EMAIL is required" && exit 1)
	docker compose run --rm --entrypoint node auth dist/cli.js reset-mfa "$(EMAIL)"
