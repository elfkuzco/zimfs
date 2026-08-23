# ===================================================================== #
# HELPERS
# ===================================================================== #
## help: print this help message and exit
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

.PHONY: confirm
confirm:
	@echo -n 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

# ===================================================================== #
# VARIABLES
# ===================================================================== #

current_time = $(shell date --iso-8601=seconds)
git_description=$(shell git describe --always --dirty --tags --long)
linker_flags='-s -X main.buildTime=${current_time} -X main.version=${git_description}'

# ===================================================================== #
# QUALITY CONTROL
# ===================================================================== #

## audit: tidy dependencies and format, vet and test all code
.PHONY: audit
audit:
	@echo 'Tidying and verifying module dependencies...'
	go mod tidy
	go mod verify
	@echo 'Formatting code...'
	go fmt ./...
	@echo 'Vetting code...'
	go vet ./...
	@echo 'Running tests...'
	go test -race -vet=off ./...

# ===================================================================== #
# BUILD
# ===================================================================== #


## build: build the application
.PHONY: build
build:
	@echo 'Building...'
	go build -ldflags=${linker_flags} ./cmd/zimfs

## install: install the application to $GOPATH/bin
.PHONY: install
install:
	@echo 'Installing...'
	bo install -ldflags=${linker_flags} ./cmd/zimfs
