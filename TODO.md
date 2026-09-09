# TODO

## MySQL and MariaDB support

**Status: not implemented.** Both engines are already *declared* in
`internal/engine/engine.go` with `Implemented: false`, so `devrig up mysql`
fails with a clear "planned" message instead of "unknown resource". Both
backends are engine-agnostic already — Docker and Kubernetes read image, port,
env and DSN from the `engine.Spec`, so filling in the spec plus a readiness
probe lights up `--backend kube` too, at no extra cost.

### 1. Fill in the specs — `internal/engine/engine.go`

Complete the `MySQL` and `MariaDB` entries and flip `Implemented: true`:

```go
Env: func(user, password, database string) []string {
    return []string{
        "MYSQL_ROOT_PASSWORD=" + password, // or MYSQL_ALLOW_EMPTY_PASSWORD=yes
        "MYSQL_USER=" + user,
        "MYSQL_PASSWORD=" + password,
        "MYSQL_DATABASE=" + database,
    }
},
URL: func(user, password, host string, port int, database string) string {
    // Go's MySQL driver wants user:pass@tcp(host:port)/db, not a URL.
    // Decide deliberately what `devrig url` prints — see §4.
},
```

Notes:

- `MYSQL_USER=root` is rejected by the official image (root already exists).
  Either validate it or map it to root-only env.
- MariaDB accepts `MARIADB_*` and still honours `MYSQL_*`. Prefer `MARIADB_*`
  for the mariadb spec, keep `MYSQL_*` for mysql.
- Both images ignore init env vars when the data dir already exists — fine
  here, since every instance starts from an empty anonymous volume.

### 2. Readiness probe — `internal/wait/wait.go`

`probeQuery` switches on `spec.Name` and currently only handles Postgres. Add a
MySQL branch:

- Add the driver: `go get github.com/go-sql-driver/mysql`, then
  `database/sql` + `sql.Open("mysql", dsn)` + `SELECT 1`.
- **TCP accept is not readiness.** MySQL binds the port during init and then
  restarts internally; connecting too early yields "Lost connection" or
  "Access denied" while users are still being created. Retry the query until
  the context expires, exactly like the Postgres path.
- Keep the MySQL import out of the Postgres path so the dependency is only
  paid by users who need it.

### 3. Reuse guard — `internal/docker/client.go`

`Up` reuses a running container when user/password/database/image/port match.
Add the engine to that comparison, otherwise `up mysql --name x` would adopt a
Postgres container that happens to carry the same name. The engine is already
persisted in the `devrig.engine` label and surfaced in `Status.Engine`. The
Kubernetes backend already compares the engine label in `matchesConfig`.

### 4. Decide the URL shape — affects callers

Postgres prints a real URL (`postgres://…?sslmode=disable`) that clients accept
verbatim. MySQL has two competing conventions:

| Form | Who accepts it |
| --- | --- |
| `mysql://user:pass@host:port/db` | JDBC, SQLAlchemy, Node, PHP |
| `user:pass@tcp(host:port)/db` | Go's `go-sql-driver/mysql` |

Recommendation: print the `mysql://` URL (it is what `TEST_DATABASE_URL`
consumers expect) and add `devrig url <name> --format go-dsn` for the Go form.
Document it in the README so nobody guesses.

### 5. Tests

- `internal/engine/engine_test.go`: the "planned engines are refused" test will
  start failing once these are implemented — that is intentional; move mysql and
  mariadb from the refused list to the implemented list.
- Add an integration test (build tag `integration`) that runs `up`/`url`/`down`
  against a real MySQL container, mirroring the Postgres one.
- Assert the DSN shape explicitly; a wrong DSN fails far away from here.

### 6. Docs

- README: resource table, `devrig up mysql` examples, default image and port.
- `skills/devrig/SKILL.md`: teach agents the new resource, and that
  `sslmode=disable` is Postgres-only (MySQL has its own TLS flags).

## Kubernetes backend

Shipped: pod + detached `kubectl port-forward`, auto-detection, `--namespace`,
`--context`, `--kubeconfig`, `--cpu`, `--memory`, `list -A`. See
[specs/kube.yaml](specs/kube.yaml). Known gaps:

- **No Service/NodePort option.** Exposure is always a local tunnel, which is
  the only thing that needs no RBAC beyond `pods/portforward`. A cluster where
  a long-lived tunnel is unwanted would want `--expose nodeport`, gated on the
  caller actually being allowed to create Services.
- **No `prune` for orphans.** A pod whose forward is gone (or whose owner walked
  away) keeps running; only `down <name>` removes it. `list` shows it with
  `ready: false` and no port.
- **The forward is per machine.** `~/.local/state/devrig/forwards` is local
  state: two people pointing at the same instance each get their own tunnel and
  their own local port, which is intended but worth stating.
- **No in-cluster mode.** For a test that runs *inside* the cluster, the useful
  output is the pod IP or a Service DNS name, not `127.0.0.1`. That is a
  different contract for `url` and is deliberately not guessed at.
- **Integration test needs a cluster.** `mise run test:integration:kube` skips
  itself when kubectl cannot reach one; set `DEVRIG_TEST_NAMESPACE` to pick the
  namespace it creates the pod in.

## Podman as an instance backend

**Deliberately not done.** Podman speaks the Docker Engine API, so pointing
`internal/docker` at the Podman socket (`CONTAINER_HOST`, `podman machine
inspect`, `podman info --format '{{.Host.RemoteSocket.Path}}'`) would give
`devrig up postgres --backend podman` for little code. It is not built because
on the machines that have Podman the tests run through kubectl: Podman is the
build-and-push tool there, and a laptop has Docker. If a Podman-only box ever
needs `up`, the work is a `Kind` value, socket discovery and a `Backend`
field of `"podman"`; nothing in the lifecycle changes.

## Smaller items

- `up` has no `--reuse=false` flag; today reuse is implicit when the config matches.
- No `prune` command for stopped instances (`list` shows them, `down` takes one name).
- `logs` does not follow; `--tail` only (both backends).
- Consider `--host` for binding beyond loopback (deliberately not exposed yet:
  these containers have throwaway credentials).
- `push` reports the digest best effort: Podman via `--digestfile`, Docker by
  parsing stdout. An empty digest is not an error.
- `build --platform` takes one platform; multi-arch manifest lists are out of
  scope.

## Beyond databases

The name is deliberate: `devrig` is "the rig your dev loop runs on", not "a
Postgres launcher". Caches, brokers and object storage fit the same lifecycle
(`up` → `url` → `down`) and the same Docker layer.

Before adding a non-database resource, settle two things:

1. ~~**`up` argument shape.**~~ **Settled in v0.2.0**: the resource comes first
   (`devrig up postgres --name <instance>`), `--engine` is gone. Old invocations
   fail with the replacement command.
2. **What `url` means for a resource with no DSN.** Redis and Postgres both have
   URLs; MinIO has an endpoint plus credentials; Kafka has a bootstrap server.
   Either `url` stays a single string per resource, or `status` becomes the
   documented interface for anything richer.

Readiness is per resource, exactly like the engine probes: TCP accept is never
enough (Redis answers PING before loading an RDB; Kafka accepts before the
broker registers).
