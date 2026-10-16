# syntax=docker/dockerfile:1
# One image recipe for every backend service: pass --build-arg SERVICE=<name>
# where <name> is a directory under cmd/ (ingest, processor, realtime,
# simulator, verify).

FROM golang:1.26-alpine AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    test -n "$SERVICE" && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE} && \
    mkdir -p /out/spool

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
# Spool directory owned by the nonroot user (uid 65532); a named volume
# mounted here inherits this ownership on first use.
COPY --from=build --chown=65532:65532 /out/spool /var/lib/uld/spool
USER nonroot:nonroot
ENTRYPOINT ["/app"]
