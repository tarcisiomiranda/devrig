# devrig

Disposable development dependencies, on demand. `devrig` starts named
instances — a **Docker container** or a **Kubernetes pod** — waits until they
really answer requests, and hands you a connection URL, then throws them away.
It also builds and pushes images with Podman or Docker, logging in from `.env`
without ever showing the token.

```bash
devrig up postgres --name myapp --db myapp_test
export TEST_DATABASE_URL="$(devrig url myapp)"
# … run your tests …
devrig down myapp
```

The resource comes first, the instance name is optional: `devrig up postgres`
starts one named `postgres`. Use `--name` when you want several, or a name that
matches the project.

On a server or a cluster where there is no Docker socket and the only tool is
`kubectl`, the same commands run a pod instead:

```bash
devrig up postgres --backend kube -n dev --name myapp
export TEST_DATABASE_URL="$(devrig url myapp)"   # 127.0.0.1, through a port-forward
devrig down myapp                                # deletes the pod, kills the tunnel
```

Built for humans and for AI coding agents: every command prints JSON, instance
names are stable, and ports are ephemeral by default so parallel projects never
collide.

Today it runs Postgres. The point of the name is that it is not limited to one
kind of resource: MySQL and MariaDB are next, and other dev dependencies
(caches, brokers, object storage) fit the same shape. See [TODO.md](TODO.md).

## Backends

| Backend | How it runs | Requirements |
| --- | --- | --- |
| `docker` | container through the Docker Engine API | Docker Desktop, OrbStack, Colima or a Linux daemon |
| `kube` | pod plus a detached `kubectl port-forward` | `kubectl` pointing at a cluster, permission to create pods and `pods/portforward` |

Without `--backend`, devrig uses Docker when the daemon answers and falls back
to Kubernetes — so the same script works on a laptop and on a k3s or GKE box
that has nothing but kubectl. `--namespace`, `--context`, `--kubeconfig`,
`--cpu` or `--memory` on their own already select the kube backend, and
`DEVRIG_BACKEND=kube` pins it for a whole shell.

`up` always creates on one backend, but `url`, `status`, `logs`, `down` and
`list` look on both, so a pod started with `--backend kube` is still found by a
bare `devrig url <name>`.

### What the kube backend creates

One pod named `devrig-<instance>`, labelled `devrig.managed=1`, with the
credentials and image kept in annotations, a TCP readiness probe on the engine
port, and cpu/memory requests and limits (`100m`/`256Mi` and `1`/`1Gi`, both
overridable with `--cpu` and `--memory`) so namespaces with a `ResourceQuota`
accept it. No Service, no PVC, no namespace is created; storage is the pod's own
disposable filesystem.

Connectivity is a `kubectl port-forward` that devrig starts detached and records
under `~/.local/state/devrig/forwards` (`$XDG_STATE_HOME` or `$DEVRIG_STATE_DIR`
if set). It survives the `devrig` process, so the URL stays valid for the whole
test run; `down` kills it. If the tunnel dies, `url` and `up` transparently open
a new one — with an ephemeral port, so read it back instead of caching it.

**The URL is local.** `127.0.0.1:<port>` only means anything on the machine that
ran `up`. Code running *inside* the cluster must reach the database by pod IP or
through a Service of its own.

## Engines

| Engine | Status | Default image | Port |
| --- | --- | --- | --- |
| `postgres` | **ready** | `postgres:16-alpine` | 5432 |
| `mysql` | planned | `mysql:8` | 3306 |
| `mariadb` | planned | `mariadb:11` | 3306 |

Planned engines are declared, so they fail with a clear message instead of
"unknown engine". See [TODO.md](TODO.md) for exactly what each one needs.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/devrig/main/install.sh | bash
```

Installs to `/usr/local/bin` when writable, otherwise `~/.local/bin`, and
verifies the release SHA-256.

**Agent skills are off by default.** To also detect AI tools on the machine
(Claude Code, Codex, OpenCode, Cursor, Grok, …) and install the `SKILL.md`
for them:

```bash
curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/devrig/main/install.sh \
  | DEVRIG_INSTALL_SKILLS=1 bash
```

From a git checkout, detect and install the same way Stacker does:

```bash
mise run skills:list          # which agents are present
mise run skills:install       # detected agents only
mise run skills:install:all   # every known tool path
```

See [skills/README.md](skills/README.md) for paths and flags.

| Variable | Effect |
| --- | --- |
| `DEVRIG_VERSION` | Install a specific tag (default: latest) |
| `DEVRIG_INSTALL_DIR` | Install somewhere else |
| `DEVRIG_INSTALL_SKILLS` | `1` to install `SKILL.md` into detected AI agents |
| `DEVRIG_COMPAT_TESTPG` | `0` to skip the `testpg` compatibility symlink |

Binaries: Linux and macOS, amd64 and arm64. Windows is not supported — use WSL2.

**Requires a Docker Engine** (Docker Desktop, OrbStack, Colima, or a Linux
daemon) for the docker backend, or a working `kubectl` for the kube backend.

### From source

```bash
mise run build     # ./bin/devrig
mise run install   # ~/.local/bin/devrig
mise run test
```

## Commands

```bash
devrig up <resource> [--name N] [--db X] [--user U] [--password P] [--port N] [--container-port N] [--image IMG]
devrig url <name>          # connection URL only (for shell substitution)
devrig status <name>       # JSON status
devrig list                # every managed instance
devrig logs <name> [--tail N]
devrig down <name>         # idempotent
devrig version
devrig build [ctx] -t IMG [-t IMG]... [-f FILE] [--platform P] [--build-arg K=V] [--target T] [--no-cache] [--push]
devrig push IMG [IMG...] [--env-file F] [--no-login] [--tool podman|docker|auto]
devrig login [REGISTRY] [--env-file F] [--user-key K] [--password-key K] [--registry-key K]
```

Backend selection is available on every command:

```bash
devrig up postgres --backend kube -n dev --name myapp [--cpu 250m] [--memory 512Mi]
devrig list --backend kube -A          # every namespace
devrig --context gke_proj_zone_cluster --namespace dev list
```

| Variable | Effect |
| --- | --- |
| `DEVRIG_BACKEND` | `docker`, `kube` or `auto` |
| `DEVRIG_NAMESPACE`, `DEVRIG_CONTEXT`, `DEVRIG_KUBECONFIG` | defaults for the kube flags |
| `DEVRIG_KUBECTL` | kubectl binary to drive (default: the one in `PATH`) |
| `DEVRIG_STATE_DIR` | where port-forward state is kept |

`up` is idempotent: if a matching instance is already running it is reused, not
recreated. If the configuration differs, the old container is replaced.

### Defaults worth knowing

- **Port is ephemeral** (`--port 0`). Always read it back with `devrig url`
  instead of hardcoding a port from a previous run. On the kube backend the port
  also changes whenever the tunnel is re-established.
- **`--port` is the host side; `--container-port` is the port inside the
  container.** The container port defaults to the engine's (5432 for Postgres)
  and only needs overriding for an `--image` that listens somewhere else. Set it
  on a stock image and the instance never becomes ready — `up` says so and names
  the port it expected.
- **Instance name** defaults to the resource name (`up postgres` → `postgres`).
- **Database name** defaults to `<instance>_test`. Projects with a convention
  should pass `--db` explicitly (Cyber uses `cyber_<system>_test`).
- **Credentials** are `test`/`test`. These containers bind to loopback and hold
  disposable data; do not put anything real in them.
- **Postgres URLs carry `?sslmode=disable`** — a local throwaway Postgres has no
  TLS, and several drivers refuse to connect unless told so explicitly.
- **`--timeout` defaults to the backend**: 30s on Docker, 3m on Kubernetes,
  where a cold node may still be pulling the image.

## Build and push

The other half of a dev loop: build an image and push it, with the same tool
split the machines already have. A laptop has Docker; a Kubernetes box has
`kubectl` plus **Podman** and no Docker socket, and there Podman is what
builds and pushes while kubectl runs things. Nobody has both.

```bash
devrig build -t ghcr.io/org/app:dev .                       # podman if present, else docker
devrig build -t ghcr.io/org/app:dev --platform linux/amd64 --push
devrig push ghcr.io/org/app:dev                              # logs in from .env, then pushes
devrig login ghcr.io                                         # just the login
```

`--tool podman|docker|auto` (or `DEVRIG_TOOL`) pins the CLI; `auto` tries
Podman first and falls back to Docker when Podman is absent or its machine is
stopped. The tool is independent from `--backend`: `DEVRIG_BACKEND=kube` and
Podman installed means "build with Podman, run on the cluster". `--backend
kube` itself never builds. Build and push output streams to **stderr**; stdout
carries one JSON object (`tool`, `tags`, `image_id`, `duration_ms`; for push
`pushed[].digest` and `logins[]`).

### Registry login from `.env`

`push` (and `build --push`) look for credentials before pushing: first in the
env file, then in the process environment. The file is `./.env` when it
exists, or `--env-file <path>`; it is read through
[confctl](https://github.com/tarcisiomiranda/confctl), so `.yaml`, `.json`
and `.toml` work too. The token is handed to `podman login --password-stdin`
(or docker's) and never appears in argv, in JSON or in an error.

| Field | Keys, in order | Override |
| --- | --- | --- |
| registry | `REGISTRY`, `CONTAINER_REGISTRY`, `DOCKER_REGISTRY` | `login --registry-key K` (push derives it from the image) |
| user | `REGISTRY_USER`, `REGISTRY_USERNAME`, `DOCKER_USER`, `DOCKER_USERNAME` | `--user-key K` |
| token | `REGISTRY_TOKEN`, `REGISTRY_PASSWORD`, `DOCKER_TOKEN`, `DOCKER_PASSWORD` | `--password-key K` |

```bash
confctl set .env REGISTRY_USER=alice REGISTRY_TOKEN=ghp_xxx   # write them
confctl -r .env                                               # look, redacted
devrig push ghcr.io/alice/app:dev                             # devrig reads them itself
```

No credentials found means push proceeds without login (the tool may hold a
session already) and `logins` stays empty. `--no-login` skips the lookup
entirely and reads no file. A registry on `localhost` or `127.0.0.1` gets
`--tls-verify=false` on Podman, which is what Docker does by default.

**confctl is required** whenever an env file is read; the error carries the
installer:

```bash
curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/confctl/main/install.sh | bash
```

| Variable | Effect |
| --- | --- |
| `DEVRIG_TOOL` | `podman`, `docker` or `auto` |
| `DEVRIG_PODMAN`, `DEVRIG_DOCKER` | binaries to drive (default: the ones in `PATH`) |
| `DEVRIG_CONFCTL` | confctl binary (default: the one in `PATH`) |

## Safety

`devrig` only ever touches containers and pods it labels itself
(`devrig.managed=1`). It also recognises containers created by its predecessor
`testpg` (`cyber.testpg=1`) so those can still be listed and removed. Nothing
else on your Docker host or in your cluster is inspected or deleted, and the
kube backend never creates a namespace, a Service or a volume.

Credentials are `test`/`test` and travel in pod annotations, readable by anyone
with `get pods` in that namespace. These are throwaway databases: keep real data
out of them, and prefer a namespace you own.

## Upgrading from v0.1.x

`up` used to take the instance name first, with the resource in `--engine`:

```bash
devrig up myapp --engine postgres   # v0.1.x
devrig up postgres --name myapp     # v0.2.0+
```

`--engine` was removed. Both old forms now fail with the exact replacement
command instead of a confusing error. Everything else (`url`, `down`, `status`,
`list`, `logs`) still takes the instance name and is unchanged.

## Relationship to `testpg`

This tool used to live inside a workspace repository as `testpg`. It was
extracted so that macOS and Linux users get prebuilt binaries instead of
building from source, and renamed because the scope is no longer "a throwaway
Postgres" — it is whatever dependency your dev loop needs, thrown away after.

The installer keeps a `testpg` symlink pointing at `devrig`, so existing
scripts, docs and muscle memory keep working. Old `testpg-*` containers remain
visible to `list` and removable with `down`.

## License

MIT.

## Releasing

Binaries are built by GitHub Actions on tag push — nothing is uploaded from a
laptop, so what users download is what CI built. The pipeline matches Stacker:
test → matrix build of four targets → checksums → publish.

```bash
# 1. write releases/vX.Y.Z.yaml (see releases/README.md)
# 2. make sure CI is green on main, then tag
git tag -a v0.1.0 -m "v0.1.0"
git push origin main
git push origin v0.1.0
```

The Release workflow:

1. Runs unit tests, `go vet`, `release:check`, `install.sh` and shellcheck.
2. Cross-compiles `linux/{amd64,arm64}` and `darwin/{amd64,arm64}` with
   `CGO_ENABLED=0`, stamping `-X main.version=<tag>`.
3. Writes a single `checksums.txt` and publishes the GitHub Release via
   `.github/scripts/release.py`.

If the tag lands but Actions does not start (a missed tag webhook), use
**Actions → Release → Run workflow** and pass the tag; `RELEASE_TAG` drives
both the binary version stamp and the release name.

Assets follow `devrig_<os>_<arch>`, which is exactly what `install.sh` resolves
from `uname`. Changing one without the other breaks installation.

Local dry-run (no publish):

```bash
mise run release:dry     # four binaries in ./dist
mise run release:check   # publisher unit tests
mise run test:installer  # install.sh against a fake release
```
