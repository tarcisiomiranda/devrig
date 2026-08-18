# devrig

Disposable development dependencies, on demand. `devrig` starts named
containers through the Docker Engine API, waits until they really answer
requests, and hands you a connection URL — then throws them away.

```bash
devrig up myapp --db myapp_test
export TEST_DATABASE_URL="$(devrig url myapp)"
# … run your tests …
devrig down myapp
```

Built for humans and for AI coding agents: every command prints JSON, instance
names are stable, and ports are ephemeral by default so parallel projects never
collide.

Today it runs Postgres. The point of the name is that it is not limited to one
kind of resource: MySQL and MariaDB are next, and other dev dependencies
(caches, brokers, object storage) fit the same shape. See [TODO.md](TODO.md).

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
verifies the release SHA-256. Also installs the agent skill on request:

```bash
curl -fsSL https://raw.githubusercontent.com/tarcisiomiranda/devrig/main/install.sh \
  | DEVRIG_INSTALL_SKILLS=1 bash
```

| Variable | Effect |
| --- | --- |
| `DEVRIG_VERSION` | Install a specific tag (default: latest) |
| `DEVRIG_INSTALL_DIR` | Install somewhere else |
| `DEVRIG_INSTALL_SKILLS` | `1` to install `SKILL.md` into detected AI agents |
| `DEVRIG_COMPAT_TESTPG` | `0` to skip the `testpg` compatibility symlink |

Binaries: Linux and macOS, amd64 and arm64. Windows is not supported — use WSL2.

**Requires a Docker Engine**: Docker Desktop, OrbStack, Colima, or a Linux daemon.

### From source

```bash
mise run build     # ./bin/devrig
mise run install   # ~/.local/bin/devrig
mise run test
```

## Commands

```bash
devrig up <name> [--db X] [--user U] [--password P] [--port N] [--image IMG] [--engine E]
devrig url <name>          # connection URL only (for shell substitution)
devrig status <name>       # JSON status
devrig list                # every managed instance
devrig logs <name> [--tail N]
devrig down <name>         # idempotent
devrig version
```

`up` is idempotent: if a matching instance is already running it is reused, not
recreated. If the configuration differs, the old container is replaced.

### Defaults worth knowing

- **Port is ephemeral** (`--port 0`). Always read it back with `devrig url`
  instead of hardcoding a port from a previous run.
- **Database name** defaults to `<name>_test`. Projects with a convention
  should pass `--db` explicitly (Cyber uses `cyber_<system>_test`).
- **Credentials** are `test`/`test`. These containers bind to loopback and hold
  disposable data; do not put anything real in them.
- **Postgres URLs carry `?sslmode=disable`** — a local throwaway Postgres has no
  TLS, and several drivers refuse to connect unless told so explicitly.

## Safety

`devrig` only ever touches containers it labels itself (`devrig.managed=1`).
It also recognises containers created by its predecessor `testpg`
(`cyber.testpg=1`) so those can still be listed and removed. Nothing else on
your Docker host is inspected or deleted.

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
laptop, so what users download is what CI built.

```bash
# 1. make sure CI is green on main, then tag
git tag -a v0.1.0 -m "v0.1.0"
git push origin main
git push origin v0.1.0
```

The workflow runs `go vet` + tests, cross-compiles `linux/{amd64,arm64}` and
`darwin/{amd64,arm64}` with `CGO_ENABLED=0`, writes `checksums.txt`, and
publishes the release with `gh` (no third-party actions).

If the tag lands but Actions does not start (a missed tag webhook), use
**Actions → Release → Run workflow** and pass the tag; the same `tag` input
drives the binary version stamp and the release name.

Assets follow `devrig_<os>_<arch>`, which is exactly what `install.sh` resolves
from `uname`. Changing one without the other breaks installation.
