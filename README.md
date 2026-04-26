# NyaMedia

NyaMedia provides a web UI and backend service for managing media providers, libraries, scan tasks, STRM outputs, and filesystem webhooks.

## Docker Compose Deployment

The repository already includes a multi-stage `Dockerfile` and a `compose.yaml` for deployment.

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

From the repository root:

```bash
docker compose up -d --build
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
    build: .
    image: nyamedia:local
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
```

### 6. Common Commands

View logs:

```bash
docker compose logs -f
```

Restart:

```bash
docker compose restart
```

Rebuild and update after pulling new code:

```bash
docker compose up -d --build
```

Stop and remove the container:

```bash
docker compose down
```

The persistent `./data` directory is not removed by `docker compose down`.
