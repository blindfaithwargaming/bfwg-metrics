# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# modernc.org/sqlite is pure Go, so the binaries are static and need no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/etl ./cmd/etl \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /out/etl /usr/local/bin/
COPY --from=build --chown=65532:65532 /out/data /data
# Synthetic fixtures let the local stack and kind demo run without member data.
COPY testdata /sample
ENV BFWG_DB_PATH=/data/bfwg-metrics.db BFWG_ADDR=:8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/usr/local/bin/server"]
