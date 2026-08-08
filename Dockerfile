# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

# Dependencies are copied first so the module download layer is cached across
# source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/diffctl ./cmd/diffctl

FROM debian:bookworm-slim

# poppler-utils provides pdftotext, which the PDF ingest path shells out to.
# It is a deliberate system dependency: "-layout" preserves reading order in the
# multi-column, table-heavy documents this tool targets far better than a
# pure-Go extractor does.
RUN apt-get update && \
    apt-get install -y --no-install-recommends poppler-utils ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Run unprivileged: the service accepts arbitrary uploaded documents.
RUN useradd --system --uid 10001 --create-home appuser
USER appuser
WORKDIR /home/appuser

COPY --from=build /out/server /usr/local/bin/server
COPY --from=build /out/diffctl /usr/local/bin/diffctl

EXPOSE 8080
ENV ADDR=:8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD ["/usr/local/bin/server", "-h"]

ENTRYPOINT ["/usr/local/bin/server"]
