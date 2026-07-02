# ClaudWorker V2 — production image (multi-stage; static binary; non-root).
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cwv2 ./cmd/cwv2

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cwv2 /usr/local/bin/cwv2
COPY --from=build /src/web/ops-console /opt/cwv2/web/ops-console
USER nonroot:nonroot
ENV CWV2_HOME=/data
VOLUME ["/data"]
EXPOSE 8080
# Simulation by default; mount config + set --mode live for production.
ENTRYPOINT ["/usr/local/bin/cwv2"]
CMD ["serve", "--config", "/data/cwv2.yaml", "--mode", "simulation", "--bind", ":8080", "--web", "/opt/cwv2/web/ops-console"]
