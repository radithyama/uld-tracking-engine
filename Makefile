.PHONY: up down reset sim sim-stop logs test lint web scan verify-stack chaos verify security-smoke seclogs

up:            ## Start infra + backend services (waits for healthchecks)
	docker compose up -d --build --wait

down:          ## Stop everything (keeps data)
	docker compose --profile sim down

reset:         ## Stop everything and wipe Postgres + spool (re-runs migrations on next up)
	docker compose --profile sim --profile tools down -v

sim:           ## Start the synthetic scanner traffic generator (30 scans/s)
	docker compose --profile sim up -d --build --wait simulator

sim-stop:
	docker compose stop simulator

logs:
	docker compose logs -f ingest processor realtime

test:          ## Backend unit tests with the race detector
	cd backend && go test -race ./...

lint:
	cd backend && go vet ./...
	cd frontend && npm run lint

web:           ## Run the dashboard (http://localhost:3000)
	cd frontend && npm run dev

verify-stack:  ## Phase 1: health + end-to-end probe across the Docker network
	scripts/verify-stack.sh

chaos:         ## Phase 2: pause + kill Kafka under load, then reconcile
	scripts/chaos-test.sh

verify:        ## Reconcile simulator ledger vs Postgres (no loss / no dupes)
	docker compose --profile tools run --rm --build verify

security-smoke: ## Phase 3: trigger every security rule and assert it was logged
	scripts/security-smoke.sh

seclogs:       ## Tail security events only
	docker compose logs -f --no-log-prefix ingest processor | jq -c 'select(.event.dataset=="uld.security")'

scan:          ## Send one sample scan
	curl -s -XPOST localhost:8080/v1/scans -H 'Content-Type: application/json' \
	  -H 'Authorization: Bearer dev-cgk-scanner-fleet-key-7f3a9c2e' \
	  -d '{"uld_id":"AKE10001GA","scanner_id":"SCN-STG-01","scan_type":"RFID","weight_kg":1420.5,"location_id":"STA-02","flight_number":"QZ7510"}'; echo
