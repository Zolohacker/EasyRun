FROM node:22-alpine AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./frontend/
RUN cd frontend && npm ci
COPY frontend ./frontend
RUN mkdir -p internal/httpapi/web/dist && cd frontend && npm run build

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/httpapi/web/dist ./internal/httpapi/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/easyrun ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 app
USER app
WORKDIR /app
COPY --from=build /out/easyrun /usr/local/bin/easyrun
EXPOSE 8080
ENV HTTP_ADDR=:8080
ENTRYPOINT ["easyrun"]
