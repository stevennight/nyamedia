# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:22-alpine AS web-build
WORKDIR /src

COPY web/package*.json ./web/
WORKDIR /src/web
RUN --mount=type=cache,target=/root/.npm \
    npm ci \
      --fetch-retries=5 \
      --fetch-retry-mintimeout=20000 \
      --fetch-retry-maxtimeout=120000

COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS go-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG NYAMEDIA_VERSION=0.1.0-dev
ARG NYAMEDIA_COMMIT=
ARG NYAMEDIA_BUILD_DATE=
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download all

COPY . .
COPY --from=web-build /src/internal/web/static ./internal/web/static
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build \
    -trimpath \
    -buildvcs=false \
    -ldflags="-s -w -X NyaMedia/internal/version.Version=${NYAMEDIA_VERSION} -X NyaMedia/internal/version.Commit=${NYAMEDIA_COMMIT} -X NyaMedia/internal/version.BuildDate=${NYAMEDIA_BUILD_DATE}" \
    -o /out/NyaMedia ./cmd/server

FROM alpine:3.21
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=go-build /out/NyaMedia ./NyaMedia

EXPOSE 7001

CMD ["./NyaMedia", "-config", "/app/configs/bootstrap.yaml"]
