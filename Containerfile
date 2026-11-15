# Development and test image: Go and this module's dependencies. The Makefile
# builds it and runs builds, tests and a shell in it, with the source mounted,
# so nothing but Podman (or Docker) is needed on the host:
#
#   make test
#
# Works with docker too: docker build -f Containerfile .
FROM docker.io/library/golang:1.25

WORKDIR /src

# Dependencies first, so they stay cached while the code changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build ./... && go vet ./...

CMD ["go", "test", "./..."]
