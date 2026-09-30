SQLC_VERSION        := v1.31.1
TEMPL_VERSION       := v0.3.1020
STATICCHECK_VERSION := 2026.2.1
TAILWIND_VERSION    := v4.3.3
GOBIN               ?= $(shell go env GOPATH)/bin

.PHONY: check vet staticcheck sqlc-diff templ-check test generate css tools dev run migrate seed ml

## check: everything CI runs. A phase is done only when this is green.
check: vet staticcheck sqlc-diff templ-check test

vet:
	go vet ./...

staticcheck:
	$(GOBIN)/staticcheck ./...

sqlc-diff:
	$(GOBIN)/sqlc diff

# templ output is committed; regenerating must not change it.
templ-check:
	$(GOBIN)/templ generate
	@test -z "$$(git status --porcelain -- '*_templ.go')" || { git status --porcelain -- '*_templ.go'; echo 'templ output is stale: run make generate and commit'; exit 1; }

test:
	go test ./...

generate:
	$(GOBIN)/sqlc generate
	$(GOBIN)/templ generate

css: bin/tailwindcss
	bin/tailwindcss -i static/css/input.css -o static/css/app.css --minify

tools: bin/tailwindcss
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	go install github.com/a-h/templ/cmd/templ@$(TEMPL_VERSION)
	go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	go install github.com/air-verse/air@latest

bin/tailwindcss:
	mkdir -p bin
	curl -sSL -o bin/tailwindcss https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-linux-x64
	chmod +x bin/tailwindcss

## dev: templ watch + Tailwind watch + air reload. Needs `docker compose up` and a .env.
dev: bin/tailwindcss
	@set -a; [ -f .env ] && . ./.env; set +a; \
	trap 'kill 0' EXIT; \
	$(GOBIN)/templ generate --watch & \
	bin/tailwindcss -i static/css/input.css -o static/css/app.css --watch & \
	$(GOBIN)/air

run: css
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/server

migrate:
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/migrate up

seed:
	@set -a; [ -f .env ] && . ./.env; set +a; go run ./cmd/seed

ml:
	cd ml && uvicorn app.main:app --host 0.0.0.0 --port 8000
