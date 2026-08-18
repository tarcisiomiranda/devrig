---
name: devrig
description: >
  Disposable dev dependencies via Docker. Use when tests or a local dev loop
  need a real database, when setting TEST_DATABASE_URL, when replacing ad-hoc
  `docker run postgres`, when a DSN (postgres://, mysql://) is needed, or when
  cleaning up throwaway containers. Commands: up, url, status, list, logs, down.
  Linux and macOS. Compatible with Claude Code, Codex, OpenCode, Cursor, Grok
  and other Agent Skills clients.
---

# devrig

Named, disposable containers for dev dependencies (Postgres today). Every
command prints JSON; `url` prints a bare connection string for shell
substitution.

## Use it instead of

- `docker run -d postgres ...` followed by guessing when it is ready
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

## Rules

1. **Never hardcode the port.** It is ephemeral by design. Always read it back
   with `devrig url <name>`.
2. **Postgres DSNs need `?sslmode=disable`.** `devrig url` already includes it.
   If you ever build a local DSN by hand, keep it — drivers fail on a local
   server without TLS otherwise.
3. **Prefer `down` (or a trap) when finished.** Leftover containers are visible
   with `devrig list`.
4. **It only touches its own containers** (label `devrig.managed=1`, plus
   legacy `cyber.testpg=1`). Never `docker rm` unrelated containers to "clean up".
5. **Docker must be running** (Docker Desktop, OrbStack, Colima, or a Linux
   daemon). If `up` fails to connect, say so — do not fall back to a system
   database.

## Commands

```bash
devrig up <resource> [--name N] [--db X] [--user U] [--password P] [--port N] [--image IMG]
devrig url <name>          # connection URL only
devrig status <name>       # JSON: state, ready, port, url
devrig list                # every managed instance
devrig logs <name> --tail 100
devrig down <name>
devrig version
```

`up` reuses a running instance when the configuration matches, so calling it at
the start of every test run is cheap and safe.

## Engines

`postgres` works today. `mysql` and `mariadb` are declared but **not
implemented** — `devrig up mysql` fails with a "planned" message. Do not tell the
user MySQL works, and do not try to work around it with a raw `docker run`;
report the limitation.

## `testpg` compatibility

This tool was called `testpg` before it grew multi-engine ambitions. The
installer keeps a `testpg` symlink, so both names work. Prefer `devrig` in new
code and documentation.

## Example: pytest with a real Postgres

```bash
devrig up postgres --name myapi --db myapi_test
export TEST_DATABASE_URL="$(devrig url myapi)"
uv run pytest -q
devrig down myapi
```

Pre-v0.2.0 syntax (`devrig up myapi --engine postgres`) is gone; the CLI prints
the replacement command if you try it.
