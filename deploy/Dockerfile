# ClaudWorker V2 — production image (multi-stage; static binary; non-root).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cwv2 ./cmd/cwv2

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chmod=0755 /out/cwv2 /usr/local/bin/cwv2
# --chmod so the console assets are world-readable regardless of source-tree perms
# (some filesystems, e.g. exFAT, present every file as owner-only 0700, which the
# non-root runtime user cannot read -> 403 on static assets). See deploy notes.
COPY --from=build --chmod=0755 /src/web/ops-console /opt/cwv2/web/ops-console
USER nonroot:nonroot
ENV CWV2_HOME=/data
VOLUME ["/data"]
EXPOSE 8080
# Simulation by default; mount config + set --mode live for production.
ENTRYPOINT ["/usr/local/bin/cwv2"]
CMD ["serve", "--config", "/data/cwv2.yaml", "--mode", "simulation", "--bind", ":8080", "--web", "/opt/cwv2/web/ops-console"]
