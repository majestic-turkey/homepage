# homepage

The landing page for **www.thesaltworks.io**. It reads the Docker daemon,
finds containers that have opted in via labels, and renders them as a list of
links. No config file to maintain: label a container, recreate it, and it shows
up within 30 seconds.

Single Go binary, no third-party dependencies, ~7 MB `scratch` image.

## How it works

```
browser ──► Caddy/nginx ──► homepage ──► docker-socket-proxy ──► /var/run/docker.sock
            (on the host)   127.0.0.1:8080   (GET /containers/json only)
```

The homepage polls `GET /containers/json`, keeps a 10-second cache, and serves
the result at `/api/services`. The page refreshes every 30 seconds.

## Security model

Two decisions are worth understanding before deploying this:

**The homepage never touches the Docker socket.** Read access to the socket is
effectively root on the host — it exposes every container's environment
variables, and write access lets anyone start a privileged container that
mounts the host filesystem. So the socket is mounted into
`tecnativa/docker-socket-proxy` instead, which is configured to allow exactly
one endpoint (`CONTAINERS=1`, `POST=0`) and 403 everything else. The proxy sits
on an `internal: true` network with no published ports, so only the homepage can
reach it. Verified: `/containers/json` → 200, `/images/json`, `/info`,
`/volumes` → 403.

**Discovery is opt-in.** A container appears only if it sets
`homepage.enable="true"`. Everything else is invisible, so the public page can't
leak your stack inventory. The JSON served to the browser also omits image
names, container ids and internal ports — only what you put in the labels goes
out.

The homepage container itself runs `read_only`, `cap_drop: ALL`,
`no-new-privileges`, as UID 65532.

## Labels

Put these on any container you want listed:

| Label | Required | Notes |
|---|---|---|
| `homepage.enable` | yes | `"true"` to opt in |
| `homepage.url` | yes\* | Link target. \*Optional if `PUBLIC_HOST` is set |
| `homepage.title` | no | Defaults to the container name |
| `homepage.description` | no | Falls back to uptime, then status |
| `homepage.icon` | no | An emoji, or an `https://` image URL |
| `homepage.group` | no | Section heading. Defaults to `Services` |
| `homepage.order` | no | Lower sorts first. Defaults to `1000` |

See [deploy/example-service.yml](deploy/example-service.yml).

An opted-in container with no resolvable URL is skipped rather than rendered as
a dead tile.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Set to `tcp://docker-socket-proxy:2375` in the compose stack |
| `PUBLIC_HOST` | *(unset)* | If set, containers without `homepage.url` get a link derived from their lowest published port |
| `SITE_TITLE` | `The Salt Works` | |
| `SITE_TAGLINE` | `Services running on this box` | |
| `SHOW_STOPPED` | `false` | Include stopped containers, rendered with a red dot |
| `CACHE_TTL` | `10s` | Bare numbers are read as seconds |

`PUBLIC_HOST` trades explicitness for convenience — it publishes the port
numbers of any opted-in container. Prefer `homepage.url` on a public page.

## Deploying

This assumes your reverse proxy runs on the host (not in Docker). The homepage
publishes on `127.0.0.1:8080`, so it is reachable from the host's proxy but not
from outside the VPS.

```sh
git clone <this repo> /opt/homepage
cd /opt/homepage
docker compose up -d --build
curl -s localhost:8080/api/services   # sanity check
```

Then point the proxy at it. [deploy/nginx.conf](deploy/nginx.conf) is the
server block for `www.thesaltworks.io`, with notes on applying it via
`certbot --nginx`. [deploy/Caddyfile](deploy/Caddyfile) is the equivalent if
you ever switch — Caddy obtains and renews certificates itself, so that file is
the entire config.

If you later move your proxy into a container, delete the `ports:` block from
`docker-compose.yml`, put the proxy and the homepage on a shared network, and
point the proxy at `homepage:8080` — then the port never leaves the daemon.

## Development

```sh
go run .                                     # uses the local Docker socket
docker build -t thesaltworks/homepage:dev .  # no local Go toolchain needed
```

The page is served from `static/`, embedded into the binary with `go:embed`, so
a rebuild is required to pick up HTML or CSS changes.
