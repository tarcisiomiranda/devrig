# devrig — instructions for AI agents

Go CLI that runs disposable development dependencies as containers through the
Docker Engine API or as Kubernetes pods through kubectl, and builds and pushes
images through the podman or docker CLI. Postgres today; the architecture is
meant to take other resources without reshaping the Docker layer. Linux and
macOS only.

## Absolute rules

- Never run `git commit`, `git push`, force-push or history rewrites — the repo
  owner commits. Finish by running `git status` / `git diff` and listing changes.
- Never publish releases, push tags or trigger the release workflow.
- Never widen what the tool deletes. It removes only containers it labelled
  (`devrig.managed=1`) or the legacy `cyber.testpg=1` ones. Do not add
  name-prefix or image-based deletion.
- Do not add credentials, hosts or bind addresses beyond loopback defaults.
- Do not hide failures with skipped tests or `|| true`.
- Never print, log or pass a registry token anywhere but the tool's stdin.
  Tests in `internal/image` assert the password is absent from argv, JSON and
  error strings; keep them passing.

## Layout

| Path | Responsibility |
| --- | --- |
| `main.go` | version wiring (`-ldflags -X main.version`) |
| `cmd/root.go` | cobra commands and flags |
| `internal/engine/` | **all engine-specific data**: image, port, env, DSN |
| `internal/docker/` | Docker Engine API: create, reuse, inspect, remove |
| `internal/kube/` | Kubernetes backend: pod + detached `kubectl port-forward` |
| `internal/rig/` | the Backend contract and JSON shapes shared by docker and kube |
| `internal/image/` | build/push/login through the podman or docker CLI; tool selection, credential keys |
| `internal/envfile/` | reads `.env`/JSON/YAML/TOML through confctl (no native parser) |
| `cmd/image.go` | `build`, `push`, `login` commands, `--tool`, env-file sources |
| `internal/instance/` | name validation, labels, container naming |
| `internal/wait/` | readiness: TCP accept **and** a real query |
| `internal/out/` | JSON stdout, fatal errors to stderr |
| `skills/devrig/` | the agent skill shipped to users |
| `scripts/install_skills.py` | detect AI agents and copy SKILL.md into their skill dirs |
| `.github/scripts/release.py` | validate artifacts and publish the GitHub Release |
| `releases/` | optional hand-written notes (`vX.Y.Z.yaml`) |

## Where engine work goes

Anything database-specific belongs in `internal/engine/engine.go` (a `Spec`) plus
a readiness branch in `internal/wait/wait.go`. The Docker layer must stay
engine-agnostic: it asks the spec for the port, the environment and the URL.

MySQL and MariaDB are declared with `Implemented: false` and fail with a clear
"planned" error. **Do not claim they work.** The full plan is in `TODO.md`.

## Invariants that tests depend on

- Postgres URLs end with `?sslmode=disable`.
- Ports are ephemeral unless `--port` is given.
- `up <resource>` takes the resource first; `--name` picks the instance
  (default: the resource name). `--engine` was removed in v0.2.0.
- `up` is idempotent: a matching running instance is reused.
- `down` on a missing instance succeeds (`removed: false, state: absent`).
- Every command emits JSON except `url`, which prints a bare string so
  `$(devrig url x)` works in a shell.
- Legacy `testpg` containers stay listable and removable, with their **real**
  labels read (never guess the database name from the instance name).
- Tool auto-detection is podman, then docker. `--tool`/`DEVRIG_TOOL` pin one.
- Build and push are not part of `rig.Backend`; `--backend kube` never builds.
- Credentials come from the env file first, then the environment, with the
  key order in `internal/image/creds.go`; the env file is read only through
  confctl.

## Commands

```bash
mise run check              # gofmt + vet + unit tests
mise run test
mise run build              # ./bin/devrig
mise run release:dry        # cross-compile all targets into ./dist
mise run release:check      # publisher unit tests + SemVer tag check
mise run test:installer     # install.sh against a fake release (no network)
mise run skills:list        # which AI agents are on this machine
mise run skills:install     # copy SKILL.md into detected agent dirs
mise run test:skills        # skill installer unit tests
```

When editing the skill body, change `skills/devrig/SKILL.md` only, then
`mise run skills:install` so the copies stay in sync.

Integration checks need a running Docker Engine. A smoke test that must keep
passing:

```bash
./bin/devrig up postgres --name smoke --db smoke_test
./bin/devrig url smoke
./bin/devrig down smoke
mise run test:integration:image     # build + push against a local registry:2
```
