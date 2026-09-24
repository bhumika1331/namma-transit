# syntax=docker/dockerfile:1
# Build: docker build -f backend/Dockerfile -t namma-transit-api .
# The image bakes the transit database in; rebuild (or set DATA_URL) to refresh data.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/loadgtfs ./cmd/loadgtfs \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/scraper ./cmd/scraper

# Data stage: if a prebuilt transit.db exists in backend/data it is used;
# otherwise the community GTFS zips are downloaded and imported.
FROM alpine:3.20 AS data
RUN apk add --no-cache curl
WORKDIR /data
COPY --from=build /out/loadgtfs /usr/local/bin/loadgtfs
COPY backend/data/ ./
RUN if [ ! -f transit.db ]; then \
      mkdir -p gtfs && \
      [ -f gtfs/bmrcl.zip ] || curl -fsSL -o gtfs/bmrcl.zip https://raw.githubusercontent.com/Vonter/bmrcl-gtfs/main/gtfs/bmrcl.zip && \
      [ -f gtfs/bmtc.zip ] || curl -fsSL -o gtfs/bmtc.zip https://raw.githubusercontent.com/Vonter/bmtc-gtfs/main/gtfs/bmtc.zip && \
      loadgtfs -db transit.db -gtfs gtfs; \
    fi && rm -rf gtfs

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /out/scraper /app/
COPY --from=data /data/transit.db /app/data/transit.db
ENV PORT=8080 DATA_SOURCE=sqlite DB_PATH=/app/data/transit.db
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/app/server"]
