FROM node:22-alpine AS web-build
WORKDIR /src

COPY web/package*.json ./web/
WORKDIR /src/web
RUN npm ci

COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS go-build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-build /src/internal/web/static ./internal/web/static
RUN go build -o /out/emby115 ./cmd/server

FROM alpine:3.21
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=go-build /out/emby115 ./emby115

EXPOSE 7001

CMD ["./emby115", "-config", "/app/configs/bootstrap.yaml"]
