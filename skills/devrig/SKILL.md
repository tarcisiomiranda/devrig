---
name: devrig
description: >
  Disposable dev dependencies via Docker or Kubernetes, plus image build and
  push through Podman or Docker. Use when tests or a local dev loop need a
  real database, when setting TEST_DATABASE_URL, when replacing ad-hoc
  `docker run postgres` or `kubectl run postgres`, when a DSN (postgres://,
  mysql://) is needed, when working on a server or cluster that has kubectl
  but no Docker socket, when an image must be built or pushed to a registry
  and the credentials live in a .env file, or when cleaning up throwaway
  instances. Commands: up, url, status, list, logs, down, build, push, login.
  Linux and macOS. Compatible with Claude Code, Codex, OpenCode, Cursor, Grok
  and other Agent Skills clients.
---

# devrig

Named, disposable instances of dev dependencies (Postgres today), as **Docker
containers** or as **Kubernetes pods**. Every command prints JSON; `url` prints
a bare connection string for shell substitution.

## Use it instead of

- `docker run -d postgres ...` followed by guessing when it is ready
- `kubectl run postgres ...` plus a hand-managed `kubectl port-forward`
- a hardcoded `TEST_DATABASE_URL` with a port copied from a previous run
- a shared/global database that other work can corrupt

## The loop

```bash
devrig up postgres --name <name> --db <database>   # create or reuse, waits until ready
export TEST_DATABASE_URL="$(devrig url <name>)"
# … run the tests …
devrig down <name>                                 # idempotent
```

The **resource comes first**; `--name` is the instance. `devrig up postgres`
alone starts one named `postgres`. Name it after the project or task so parallel
work does not collide.

## Backends

| Backend | What it does | When |
| --- | --- | --- |
| `docker` | container through the Docker Engine API | a machine with a Docker daemon |
| `kube` | a pod plus a detached `kubectl port-forward` | k3s, GKE, any cluster where the only tool is kubectl |

```bash
devrig up postgres --backend kube -n dev --name myapp
export TEST_DATABASE_URL="$(devrig url myapp)"   # 127.0.0.1:<random>, through the tunnel
devrig down myapp                                # deletes the pod and kills the tunnel
```

Without `--backend`, devrig uses Docker when the daemon answers and falls back
to kubectl. Read commands (`url`, `status`, `logs`, `down`) look on both, so a
kube instance is still found by a bare `devrig url <name>`. `--namespace`,
`--context`, `--kubeconfig`, `--cpu` or `--memory` on their own already imply
the kube backend. `DEVRIG_BACKEND=kube` pins it for a whole shell.

## Rules

1. **Never hardcode the port.** It is ephemeral by design. Always read it back
   with `devrig url <name>`. `--port` picks the host port; leave
   `--container-port` alone unless a custom `--image` listens off the default.
2. **Postgres DSNs need `?sslmode=disable`.** `devrig url` already includes it.
   If you ever build a local DSN by hand, keep it — drivers fail on a local
   server without TLS otherwise.
3. **Prefer `down` (or a trap) when finished.** Leftover containers are visible
   with `devrig list`.
4. **It only touches its own containers** (label `devrig.managed=1`, plus
   legacy `cyber.testpg=1`). Never `docker rm` unrelated containers to "clean up".
5. **Docker must be running** (Docker Desktop, OrbStack, Colima, or a Linux
   daemon) for the docker backend. If `up` fails to connect, say so — do not
   fall back to a system database.
6. **On the kube backend the URL is a local tunnel.** It only works on the
   machine that ran `up`: the port-forward is a detached `kubectl` process
   recorded under `~/.local/state/devrig/forwards`. Never hand that URL to
   something running inside the cluster — a pod there reaches the database by
   its own pod IP or a Service, not through 127.0.0.1.
7. **The kube backend needs `create pods` and `create pods/portforward`.** It
   creates no Service, no PVC and no namespace. Check with
   `kubectl auth can-i create pods`, and pass `-n <namespace>`; the pod carries
   `devrig.managed=1` and modest cpu/memory requests and limits.
8. **`down` is what removes the pod.** A leftover pod keeps running on someone
   else's cluster — always `down` (or trap it) when the test run ends.

## Commands

```bash
devrig up <resource> [--name N] [--db X] [--user U] [--password P] [--port N] [--image IMG]
devrig up <resource> --container-port N   # only for an --image that listens off the engine default
devrig up <resource> --backend kube -n <namespace> [--cpu 250m] [--memory 512Mi]
devrig list -A                            # kube backend: every namespace
devrig url <name>          # connection URL only
devrig status <name>       # JSON: state, ready, port, url
devrig list                # every managed instance
devrig logs <name> --tail 100
devrig down <name>
devrig version
devrig build [ctx] -t IMG [--platform linux/amd64] [--push]   # podman if present, else docker
devrig push IMG [--env-file F] [--no-login]                    # logs in from .env, then pushes
devrig login [REGISTRY] [--user-key K] [--password-key K]      # never prints the token
```

`up` reuses a running instance when the configuration matches, so calling it at
the start of every test run is cheap and safe.

## Build and push

```bash
devrig build -t ghcr.io/org/app:dev .            # output on stderr, JSON on stdout
devrig push ghcr.io/org/app:dev                   # reads ./.env through confctl, logs in, pushes
devrig build -t ghcr.io/org/app:dev --push        # both in one go
```

Rules:

1. **Do not choose between `docker` and `podman` by hand.** `devrig build`
   and `devrig push` pick the tool: Podman when it is installed and answers,
   Docker otherwise. A machine with kubectl and Podman builds with Podman
   and runs instances with `--backend kube`; those are the two halves of the
   loop.
2. **Never `cat .env`, never `echo $TOKEN`, never `docker login -p <token>`.**
   devrig reads the user and the token itself and hands the token to the tool
   on stdin. To see which keys a file has, run `confctl -r .env` (redacted).
   To add them, run `confctl set .env REGISTRY_USER=<user> REGISTRY_TOKEN=<token>`
   with values the user gives you, never values you found elsewhere.
3. **Credential keys**, in order: user `REGISTRY_USER`, `REGISTRY_USERNAME`,
   `DOCKER_USER`, `DOCKER_USERNAME`; token `REGISTRY_TOKEN`,
   `REGISTRY_PASSWORD`, `DOCKER_TOKEN`, `DOCKER_PASSWORD`. The env file wins
   over the shell environment. A project with other names gets
   `--user-key`/`--password-key`; do not rename its keys to fit devrig.
4. **If login or push fails, report the error verbatim and stop.** Do not
   retry with a token pasted on the command line, do not switch registries,
   do not "try docker instead".
5. **Never put a secret in `--build-arg`.** It ends up in the image history.
6. **confctl missing** is a real error with the installer in it; tell the
   user, do not fall back to parsing `.env` yourself.
7. **The JSON never contains the token.** `logins[]` names the registry, the
   user and the *key* the token came from; that is all you should ever quote.

## Engines

`postgres` works today. `mysql` and `mariadb` are declared but **not
implemented** — `devrig up mysql` fails with a "planned" message. Do not tell the
user MySQL works, and do not try to work around it with a raw `docker run`;
report the limitation.

## `testpg` compatibility

This tool was called `testpg` before it grew multi-engine ambitions. The
installer keeps a `testpg` symlink, so both names work. Prefer `devrig` in new
code and documentation.

## Example: tests on a server that only has kubectl

```bash
devrig up postgres --backend kube -n dev --name ci-check
export TEST_DATABASE_URL="$(devrig url ci-check)"
uv run pytest -q
devrig down ci-check
```

The first `up` on a cold node waits for the image pull; the timeout defaults to
3m there (30s on Docker) and `--timeout` overrides it.

## Example: pytest with a real Postgres

```bash
devrig up postgres --name myapi --db myapi_test
export TEST_DATABASE_URL="$(devrig url myapi)"
uv run pytest -q
devrig down myapi
```

Pre-v0.2.0 syntax (`devrig up myapi --engine postgres`) is gone; the CLI prints
the replacement command if you try it.
