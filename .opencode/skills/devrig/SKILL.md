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
  instances, executing commands in managed containers/pods, running helper
  images, executing TXT command lists, or passing complex arguments and
  stdin without shell quoting.
  Commands: up, url, status, list, logs, down, exec, run, build, push, login.
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

For test-only dependencies, run setup, tests and cleanup in **one Bash
invocation**. Replace `uv run pytest -q` with the project's test command:

```bash
(
  set -euo pipefail
  devrig_name="myapi-$(date +%s)-$$-$RANDOM"
  devrig_scope=(--backend docker)

  cleanup_devrig() {
    devrig_result=$?
    trap - EXIT
    if devrig down "$devrig_name" "${devrig_scope[@]}"; then
      exit "$devrig_result"
    else
      devrig_cleanup_result=$?
      printf 'Cleanup failed for devrig instance %s\n' "$devrig_name" >&2
      if [ "$devrig_result" -ne 0 ]; then
        exit "$devrig_result"
      fi
      exit "$devrig_cleanup_result"
    fi
  }

  trap cleanup_devrig EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM

  devrig up postgres --name "$devrig_name" --db myapi_test "${devrig_scope[@]}"
  TEST_DATABASE_URL="$(devrig url "$devrig_name" "${devrig_scope[@]}")"
  export TEST_DATABASE_URL
  uv run pytest -q
)
```

The **resource comes first**; `--name` is the instance. `devrig up postgres`
alone starts one named `postgres`. For tests, use a short project/task prefix
and a unique run suffix so parallel sessions do not reuse each other's database.

## Lifetime and cleanup

`up` leaves the database running after the CLI exits. It has **no session-end
hook, TTL or lease**: ending an agent/harness session does not call `down`.
The automatic cleanup in `run` applies only to its helper container, never to
the database supplied through `--connect`.

- **Remove test instances owned by the task as soon as tests finish**, on
  success or failure, and before the final response. Keep one running only
  when the user requested continued use; report its name and exact `down`
  command. Do not defer cleanup until the harness session closes.
- **Install cleanup before `up`.** Use a trap as above, or a test fixture's
  `finally`/teardown. A trap belongs to one shell invocation, not to the
  conversation: it cannot protect later, separate tool calls. For a workflow
  spanning tool calls, retain the exact instance names and backend settings
  in task/handoff context and explicitly clean up on completion or failure.
- **Clean only the task's own instances.** A managed label identifies devrig,
  not the owning session. Do not remove pre-existing/shared instances or
  sweep `devrig list`; choose a fresh name unless reuse was requested. Reuse
  alone does not authorize deleting an instance owned by another workflow.
- **Keep the same backend and Kubernetes scope for cleanup.** Record the
  backend, namespace, context and kubeconfig used by `up`; pass the same
  values to `url` and `down`, even if shell defaults change between calls.
- **Verify cleanup and preserve failures.** Successful `down` reports
  `state: absent`; `removed: false` also succeeds when already absent. Do
  not mask test failures or suppress cleanup errors. If cleanup fails,
  report the remaining name, scope and retry command in the final response.

Traps cannot run after `SIGKILL`, a host crash, or if the harness never exits
or signals that shell. Abrupt session loss needs an external lifecycle hook
or a future lease/TTL mechanism; this skill cannot guarantee it.

## Backends

| Backend | What it does | When |
| --- | --- | --- |
| `docker` | container through the Docker Engine API | a machine with a Docker daemon |
| `kube` | a pod plus a detached `kubectl port-forward` | k3s, GKE, any cluster where the only tool is kubectl |

```bash
devrig_scope=(--backend kube --namespace dev)
```

Use that scope in the guarded loop above. Include `--context` and
`--kubeconfig` when needed and retain their values for cleanup. The URL uses
127.0.0.1 through the tunnel; `down` deletes the pod and stops the tunnel.

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
3. **Cleanup is part of the test run.** Follow the lifetime rules above;
   `devrig list` shows managed instances, not permission to delete them all.
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
devrig exec <name> [--request FILE] [--stdin-file FILE] -- COMMAND [ARG...]
devrig run IMAGE [--connect NAME] [--request FILE] [--stdin-file FILE] -- COMMAND [ARG...]
devrig exec <name> --commands-file FILE.txt
devrig run IMAGE [--connect NAME] --commands-file FILE.txt
devrig version
devrig build [ctx] -t IMG [--platform linux/amd64] [--push]   # podman if present, else docker
devrig push IMG [--env-file F] [--no-login]                    # logs in from .env, then pushes
devrig login [REGISTRY] [--user-key K] [--password-key K]      # never prints the token
```

`up` reuses a running instance when the configuration matches. Reuse the same
name only within a workflow that owns its lifecycle or when the user requests
it; independent test runs need distinct names.

## Commands inside instances and helper images

Choose the input format by its contents, not its file extension:

| Contents | Use |
| --- | --- |
| One executable command per TXT line | `--commands-file commands.txt` |
| Complex argv/env or multiline input | `--request request.json` |
| SQL statements in a TXT or SQL file | `--stdin-file commands.txt -- psql -v ON_ERROR_STOP=1` |

Use `devrig exec` instead of `docker exec` or `kubectl exec` for managed
instances. It works on Docker and Kubernetes, looks up the real ownership
labels and sets the engine's client environment. For Postgres that includes
the actual user, password, database and container port, with
`PGHOST=127.0.0.1` and `PGSSLMODE=disable`.

```bash
devrig exec myapp -- psql -At -c 'SELECT 1'
devrig exec myapp --stdin-file migration.sql -- psql -v ON_ERROR_STOP=1
```

Use `devrig run` instead of an ad-hoc `docker run` for helper commands in a
specific image, such as validating or restoring a dump with a backup image.
It uses the Docker Engine API and automatically removes only its own helper
after completion, failure, interruption or timeout. `--connect NAME` shares
a managed instance's network and derives the client environment from its
actual configuration, so the helper connects over loopback.

```bash
devrig run backup:local --stdin-file fixture.dump -- pg_restore --list
devrig run backup:local --connect myapp --stdin-file fixture.dump -- pg_restore --exit-on-error --no-owner --no-acl --dbname=myapp_test
```

Rules:

1. **Prefer structured requests for complex input.** Write a JSON request
   file with `argv` as an array, `env` as an object, and either `stdin` as a
   string or `stdin_file`. Use `--request FILE`; `--request -` accepts JSON
   on stdin. Do not concatenate user input into a shell command. devrig
   passes each argument and stdin directly, preserving quotes, `$`, spaces,
   backticks, newlines, backslashes and Unicode. SQL and other receiving
   programs still interpret their own input syntax.
2. **No implicit shell.** Use `-- COMMAND ARG...` for simple calls. With
   `run`, the command replaces the image's entrypoint; omitting it uses the
   image defaults. Shell syntax only works if you explicitly invoke a shell.
3. **Read the JSON and exit status.** `stdout`, `stderr` and `exit_code` are
   separate. The CLI also exits with the command's failure code. Invalid
   UTF-8 is returned as `stdout_base64`/`stderr_base64`; never treat a failed
   command as success just because it produced output.
4. **Stream dumps through files.** `--stdin-file` preserves binary bytes.
   Use `--stdout-file`/`--stderr-file` for large or binary output, also
   available as request fields. Output files are mode `0600`, are never
   overwritten and can contain partial data on failure. Request file paths
   are relative to the JSON file; flag paths are relative to the current
   directory. Do not put binary dumps through shell variables or JSON text.
5. **One stdin source.** `-i`/`--stdin-file -` reads command stdin without a
   TTY. Never combine it with `--request -`; put input in request `stdin` or
   a file. Positional arguments and request `argv` are mutually exclusive.
6. **Use `--connect`, not generated container names or host ports.** The
   helper inherits the target's client connection settings; explicit `env`
   or repeatable `--env KEY=VALUE` values override defaults. `--name` is
   optional; names in use are never replaced by `run`.
7. **Backend limits are explicit.** `exec` works on both backends; `run`
   and `exec --workdir` currently require Docker. On kube report that limit
   instead of silently switching to a raw container command. Kubernetes
   `exec` needs `create pods/exec` permission and the image's `env` program.
8. **Timeouts differ from instance lifetime.** `--timeout` is optional and
   defaults to no limit for these commands. Interrupted/timed-out `run`
   removes the helper. Interrupted/timed-out `exec` detaches; the command
   inside the existing instance may keep running.

Example `.build/restore.json`, next to `fixture.dump`:

```json
{
  "connect": "myapp",
  "argv": ["pg_restore", "--exit-on-error", "--no-owner", "--no-acl", "--dbname=myapp_test"],
  "stdin_file": "fixture.dump"
}
```

```bash
devrig run backup:local --request .build/restore.json
```

`exec` requests accept `argv`, `env`, `stdin`, `stdin_file`, `stdout_file`,
`stderr_file` and `workdir`. `run` also accepts `name` and `connect`. Flags
override corresponding fields. Unknown fields, trailing JSON, invalid env
names and NUL in arguments/env values are errors. Requests and env values
are not echoed in the result, but the command's own output is returned.

### Lists of commands in a text file

For several simple commands, use `--commands-file FILE.txt` with one command
per physical line, or `--commands-file -` to read the list from stdin:

```text
psql -At -c "SELECT current_database()"
psql -At -c "SELECT 'literal $HOME'"
```

```bash
devrig exec myapp --commands-file commands.txt
devrig run postgres:16-alpine --connect myapp --commands-file commands.txt
```

The entire file is validated first. Lines execute in order and stop on the
first failure. Read overall `exit_code`, `total` and `results[]`; each result
includes its physical `line`, stdout, stderr and exit_code. Earlier results
remain available if a later command fails.

Blank lines and unquoted `#` comments at a word boundary are ignored. Single
and double quotes group arguments; empty quoted arguments are preserved.
Outside quotes, backslash escapes the next character. In double quotes it
escapes `$`, backticks, `"` and `\`; other backslashes remain literal.
Variables, backticks, substitutions and globs never expand. Unquoted shell
operators (`;`, `|`, `&`, `<`, `>`) are errors; invoke a shell explicitly with
a quoted script when shell behavior is intended. Use JSON/stdin files for
multiline input. The text must be UTF-8; CRLF and a leading BOM work.

Do not combine this mode with `--request`, positional command arguments,
`-i` or `--stdin-file -`. `--stdin-file FILE` is replayed for each command;
env/workdir apply to all commands, and timeout applies to the whole batch.
Output files concatenate command bytes in order. Each exec line is a new
process in the same instance; each run line is a new disposable helper, so
helper filesystem changes do not carry over. For pure SQL, pass the file
through `--stdin-file script.sql -- psql -v ON_ERROR_STOP=1` instead.

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

Use the guarded test loop above with the kube scope and the target namespace.
Keep `up`, URL lookup, tests and the cleanup trap in the same Bash invocation.

The first `up` on a cold node waits for the image pull; the timeout defaults to
3m there (30s on Docker) and `--timeout` overrides it. That timeout only bounds
startup readiness; it does not expire the running database.

Pre-v0.2.0 syntax (`devrig up myapi --engine postgres`) is gone; the CLI prints
the replacement command if you try it.
