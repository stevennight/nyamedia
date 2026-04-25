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
RUN go build -o /out/NyaMedia ./cmd/server

FROM alpine:3.21
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=go-build /out/NyaMedia ./NyaMedia

EXPOSE 7001

CMD ["./NyaMedia", "-config", "/app/configs/bootstrap.yaml"]
