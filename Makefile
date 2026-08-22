.PHONY: up down logs test test-db-up test-db-down lint migrate-up migrate-down gen-dev-cert seed preflight create-admin reset-password reset-mfa

# Local development builds from source; production pulls what CI published.
up:
	docker compose -f docker-compose.yaml -f docker-compose.build.yaml --env-file .env up --build

down:
	docker compose down -v

logs:
	docker compose logs -f

# TEST_DATABASE_URL is what decides whether the suites that assert against
# real rows actually run. Without it the Go postgres tests t.Skip and the auth
# suite skips 35 of its 72 - and both report success, so "make test" can print
# a clean run while the tests that would have caught a schema change never
# executed. That is not hypothetical: a folder added to createMailbox passed
# here and failed in CI, which does set the variable.
test:
	@if [ -z "$$TEST_DATABASE_URL" ]; then 		echo "=============================================================="; 		echo "TEST_DATABASE_URL is not set."; 		echo "The database-backed tests will SKIP, and the suites will still"; 		echo "report success. CI sets it, so a green run here does not mean"; 		echo "a green run there. Start one with:"; 		echo "  make test-db-up"; 		echo "=============================================================="; 	fi
	cd services/protocols && go test ./...
	cd services/auth && npm test
	cd services/storage && npm test
	cd services/webmail && npm test

# Brings up a throwaway Postgres with every migration applied, and prints the
# URL to export. Matches what CI provisions, so "make test" with this exported
# runs the same set of tests CI does.
test-db-up:
	docker rm -f lambdamail-test-db 2>/dev/null || true
	docker run -d --name lambdamail-test-db \
		-e POSTGRES_USER=lambdamail_test \
		-e POSTGRES_PASSWORD=lambdamail_test \
		-e POSTGRES_DB=lambdamail_test \
		-p 55432:5432 postgres:16-alpine
	@# pg_isready alone is not enough: the postgres entrypoint brings the
	@# server up once to run its init scripts and then restarts it, so there is
	@# a window where it answers ready and then drops the connection. A real
	@# query is what proves it is actually serving.
	@until docker exec lambdamail-test-db psql -U lambdamail_test -d lambdamail_test -c "SELECT 1" >/dev/null 2>&1; do sleep 1; done
	docker run --rm --network host -v "$(PWD)/migrations:/migrations" \
		migrate/migrate:v4.17.1 -path=/migrations \
		-database "postgres://lambdamail_test:lambdamail_test@localhost:55432/lambdamail_test?sslmode=disable" up
	@echo ""
	@echo "Ready. Export this, then run make test:"
	@echo "  export TEST_DATABASE_URL='postgres://lambdamail_test:lambdamail_test@localhost:55432/lambdamail_test?sslmode=disable'"
	@echo "  export LAMBDAMAIL_MASTER_KEY='local-master-key-at-least-16-chars'"

test-db-down:
	docker rm -f lambdamail-test-db 2>/dev/null || true

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
