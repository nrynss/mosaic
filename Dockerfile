# This image packages cmd/mosaicdemo, the intentionally small runtime
# composition root. P12 owns the packaging and acceptance boundary, not the
# application composition itself.
FROM node:22-bookworm-slim AS dashboard-build

WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

FROM golang:1.24.5-bookworm AS runtime-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/mosaicdemo/ ./cmd/mosaicdemo/
COPY internal/ ./internal/
COPY migrations/ ./migrations/
COPY ontology/ ./ontology/
COPY datasets/domestic-disturbance/ ./datasets/domestic-disturbance/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mosaicdemo ./cmd/mosaicdemo

FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /srv/mosaic
COPY --from=runtime-build --chown=nonroot:nonroot /out/mosaicdemo /usr/local/bin/mosaicdemo
COPY --from=runtime-build --chown=nonroot:nonroot /src/ontology ./ontology
COPY --from=runtime-build --chown=nonroot:nonroot /src/datasets/domestic-disturbance ./datasets/domestic-disturbance
COPY --from=dashboard-build --chown=nonroot:nonroot /src/ui/dist ./ui

# Do not set MOSAIC_LISTEN_ADDR here. Leaving it empty preserves the process
# PORT fallback (0.0.0.0:${PORT}) required by Cloud Run's dynamic port. Local
# Compose sets MOSAIC_LISTEN_ADDR=:8080 explicitly.
#
# Do not set MOSAIC_DB_PATH here. Local Compose injects a postgres:// DSN to the
# db service; Cloud Run / single-process local runs pass an explicit SQLite path
# or DSN. A baked-in /var/lib/mosaic volume would misrepresent this image as a
# stateful SQLite appliance — the Compose topology is stateless app + Postgres.
ENV MOSAIC_UI_DIR=/srv/mosaic/ui

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mosaicdemo"]
