# Builds, tests and a dev shell in a container (see Containerfile), with the
# source mounted: nothing but Podman is needed on the host. Use Docker instead
# with: make test ENGINE=docker
ENGINE ?= podman
IMAGE ?= localhost/eml2html-dev
RUN = $(ENGINE) run --rm -v "$(CURDIR)":/src:Z -w /src $(IMAGE)

.PHONY: help image test vet fmt-check fmt build shell clean

help:  ## List the targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/'

image:  ## Build the dev and test image
	$(ENGINE) build -t $(IMAGE) -f Containerfile .

test: image  ## Run the tests (go test, with the race detector)
	$(RUN) go test -race ./...

vet: image  ## go vet
	$(RUN) go vet ./...

fmt-check: image  ## List files gofmt would change (the original files predate Go 1.19's doc-comment style)
	$(RUN) gofmt -l .

fmt: image  ## Format the code with gofmt
	$(RUN) gofmt -w .

build: image  ## Build a static binary into ./bin/eml2html
	$(RUN) sh -c 'mkdir -p bin && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/eml2html ./cmd/eml2html'

shell: image  ## A shell in the dev container
	$(ENGINE) run --rm -it -v "$(CURDIR)":/src:Z -w /src $(IMAGE) bash

clean:  ## Remove ./bin
	rm -rf bin
