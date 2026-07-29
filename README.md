# NyaMedia

NyaMedia provides a web UI and backend service for managing media providers, libraries, scan tasks, STRM outputs, and filesystem webhooks.

Supported providers include local files, `115cookie`, `115open`, and `123pan`.
The 123pan integration uses official Open Platform Client ID/Client Secret
credentials; see [docs/123pan-provider.md](docs/123pan-provider.md).

## Docker Compose Deployment

Tagged releases are built by GitHub Actions for Linux amd64 and arm64 and published to:

```text
ghcr.io/stevennight/nyamedia
```

The server only pulls and runs the image; it does not compile Go or build the
admin UI.

### 1. Prepare The Config

Create the runtime config from the example:

```bash
cp configs/bootstrap.example.yaml configs/bootstrap.yaml
```

Edit `configs/bootstrap.yaml` for production:

```yaml
server:
  host: 0.0.0.0
  port: 7001
  public_base_url: https://your-domain.example
  proxy_base_urls: []

storage:
  data_dir: /app/data
  database_url: postgres://nyamedia:nyamedia@postgres:5432/nyamedia?sslmode=disable
  strm_output_dir: /app/data/strm

auth:
  bootstrap_username: admin
  bootstrap_password: change-this-password

webhook:
  token: change-this-to-a-strong-random-token

logging:
  level: info
```

Important production notes:

- Change `auth.bootstrap_password` before exposing the service.
- Set a strong `webhook.token` if webhook endpoints are exposed.
- Set `server.public_base_url` to the final public URL if the service is behind a reverse proxy.

### 2. Start The Service

Copy the deployment environment file and select a released version:

```bash
cp .env.example .env
```

```dotenv
NYAMEDIA_IMAGE=ghcr.io/stevennight/nyamedia
NYAMEDIA_VERSION=v0.1.0
```

Then pull and start:

```bash
docker compose pull
docker compose up -d
```

The default compose file exposes the service on port `7001`:

```text
http://SERVER_IP:7001
```

### 3. Persistent Data

The default `compose.yaml` persists app data to `./data` on the host:

```yaml
services:
  nyamedia:
    image: ${NYAMEDIA_IMAGE:-ghcr.io/stevennight/nyamedia}:${NYAMEDIA_VERSION:-latest}
    container_name: nyamedia
    restart: unless-stopped
    ports:
      - "7001:7001"
    volumes:
      - ./configs/bootstrap.yaml:/app/configs/bootstrap.yaml:ro
      - ./data:/app/data
```

The default Compose deployment stores PostgreSQL data in the `postgres-data` Docker volume and generated STRM output in `./data`.

### 4. Mount Local Media Directories

If you use a local provider, the container must be able to see the media directory. Add an extra volume mount:

```yaml
services:
  nyamedia:
    volumes:
      - ./configs/bootstrap.yaml:/app/configs/bootstrap.yaml:ro
      - ./data:/app/data
      - /mnt/media:/media:ro
```

Then configure the provider root path in the web UI as the container path, for example:

```text
/media
```

### 5. Reverse Proxy

For HTTPS, place NyaMedia behind a reverse proxy such as Nginx, Caddy, Traefik, or a cloud load balancer.

Make sure the proxy forwards traffic to:

```text
http://127.0.0.1:7001
```

Also set `server.public_base_url` in `configs/bootstrap.yaml` to the external URL, for example:

```yaml
server:
  public_base_url: https://media.example.com
  # Trust these proxy addresses when rewriting managed Emby playback URLs.
  # A path prefix such as https://proxy.example.com/nyamedia is supported.
  proxy_base_urls:
    - https://proxy.example.com
```

`server.proxy_base_urls` is an explicit allowlist for proxy addresses supplied by the request or forwarded headers. Leave it empty unless Emby clients access NyaMedia through one of those public proxy addresses.

### 6. Common Commands

View logs:

```bash
docker compose logs -f
```

Restart:

```bash
docker compose restart
```

Update to the version selected in `.env`:

```bash
docker compose pull
docker compose up -d
```

Stop and remove the container:

```bash
docker compose down
```

The persistent `./data` directory is not removed by `docker compose down`.

### 7. Release And Local Builds

Push a version tag to publish a multi-architecture image:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow creates a GitHub Release and publishes `v0.1.0`, `0.1.0`,
`0.1`, and `latest` image tags. No additional repository secret is required;
GitHub's built-in token publishes to GHCR. The package must be public for
unauthenticated server pulls.

Use a SemVer prerelease tag to publish a preview without changing the stable
`latest` image:

```bash
git tag v0.2.0-pre.1
git push origin v0.2.0-pre.1
```

Prerelease tags create a GitHub Prerelease and publish the exact version tags
plus `pre-latest`. They do not publish the stable minor tag or update `latest`.
Set `NYAMEDIA_VERSION=pre-latest` to follow the preview channel, or use an exact
tag such as `v0.2.0-pre.1` for a reproducible deployment.

For development, build from local source with the override file:

```bash
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

Display the version embedded in a local binary or image:

```bash
go run ./cmd/server --version
docker run --rm ghcr.io/stevennight/nyamedia:v0.1.0 --version
```
