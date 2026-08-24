DEVX ?= ./cli/devx

.DEFAULT_GOAL := menu

.PHONY: menu help install uninstall doctor status completion \
	build build-exp build-purego clean demo tui serve full \
	db-up db-down db-reset db-status migrate migrate-create migrate-check graph-check \
	proto-gen proto-check policy-compile policy-check results-gen results-check \
	test test-unit test-integration test-e2e race fuzz \
	bench bench-compare profile perf load debug debug-dap debug-tui \
	docker-build docker-run docker-full

menu:
	@$(DEVX)

help:
	@$(DEVX) --help

install:
	@$(DEVX) install

uninstall:
	@$(DEVX) uninstall

doctor:
	@$(DEVX) doctor

status:
	@$(DEVX) status

completion:
	@$(DEVX) completion

build:
	@$(DEVX) build

build-exp:
	@$(DEVX) build:exp

build-purego:
	@$(DEVX) build:purego

clean:
	@$(DEVX) clean

demo:
	@$(DEVX) demo

tui:
	@$(DEVX) tui

serve:
	@$(DEVX) serve

full:
	@$(DEVX) full

db-up:
	@$(DEVX) db:up

db-down:
	@$(DEVX) db:down

db-reset:
	@$(DEVX) db:reset

db-status:
	@$(DEVX) db:status

migrate:
	@$(DEVX) migrate

migrate-create:
	@$(DEVX) migrate:create --name "$(NAME)"

migrate-check:
	@$(DEVX) migrate:check

graph-check:
	@$(DEVX) graph:check

proto-gen:
	@$(DEVX) proto:gen

proto-check:
	@$(DEVX) proto:check

policy-compile:
	@$(DEVX) policy:compile

policy-check:
	@$(DEVX) policy:check

results-gen:
	@$(DEVX) results:gen

results-check:
	@$(DEVX) results:check

test:
	@$(DEVX) test

test-unit:
	@$(DEVX) test:unit

test-integration:
	@$(DEVX) test:integration

test-e2e:
	@$(DEVX) test:e2e

race:
	@$(DEVX) test:race

fuzz:
	@$(DEVX) fuzz

bench:
	@$(DEVX) bench

bench-compare:
	@$(DEVX) bench:compare

profile:
	@$(DEVX) profile

perf:
	@$(DEVX) perf

load:
	@$(DEVX) load

debug:
	@$(DEVX) debug

debug-dap:
	@$(DEVX) debug:dap

debug-tui:
	@$(DEVX) debug:tui

docker-build:
	@$(DEVX) docker:build

docker-run:
	@$(DEVX) docker:run

docker-full:
	@$(DEVX) docker:full
