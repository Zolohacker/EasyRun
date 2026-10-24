FROM node:22-alpine AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./frontend/
RUN cd frontend && npm ci
COPY frontend ./frontend
RUN mkdir -p internal/httpapi/web/dist && cd frontend && npm run build

FROM golang:1.23-bookworm AS build
ARG VERSION=0.1.4
ARG COMMIT=source
ARG BUILT_AT=unknown
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/httpapi/web/dist ./internal/httpapi/web/dist
RUN CGO_ENABLED=1 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w -X github.com/Zolohacker/EasyRun/internal/buildinfo.Version=${VERSION} -X github.com/Zolohacker/EasyRun/internal/buildinfo.Commit=${COMMIT} -X github.com/Zolohacker/EasyRun/internal/buildinfo.BuiltAt=${BUILT_AT} -X github.com/Zolohacker/EasyRun/internal/buildinfo.BuildType=container" -o /out/easyrun ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata libstdc++6 libgomp1 && rm -rf /var/lib/apt/lists/* && useradd -M -u 10001 app && mkdir -p /app/data /app/runtime && chown app:app /app/data /app/runtime
USER app
WORKDIR /app
COPY --from=build /out/easyrun /usr/local/bin/easyrun
COPY models /app/models
EXPOSE 8080
ENV HTTP_ADDR=:8080 EASYRUN_RUNTIME_DIR=/app/runtime
VOLUME ["/app/runtime"]
ENTRYPOINT ["/usr/local/bin/easyrun"]
CMD ["--container-supervise"]
