# Bounded bridge startup and preservation-first recovery

This branch fixes startup lifecycle and diagnostic defects; it does not claim
that a real WhatsApp account is paired or that an existing cache is repaired.

## Changes

- Pairing is disabled unless `WHATSAPP_ALLOW_PAIRING` is exactly `true`.
- Missing session waits for operator intervention without opening a QR channel.
- Opening an existing authentication store additionally requires the exact
  `WHATSAPP_ALLOW_SESSION_UPGRADE=true` opt-in, because the SDK automatically
  upgrades its schema when opening it. The default uses file metadata only and
  reports `SESSION_STORE_REVIEW_REQUIRED` without reading or writing that store.
- Channel initialization, connection and event wait share a bounded startup
  deadline. Terminal events are distinct fixed error categories.
- Successful startup preserves the SDK connection context until shutdown.
- Logged-out sessions cannot use the existing send/download handlers.
- Existing message caches must pass a read-only integrity preflight. Failure
  stops before cache/auth-store writes or a network connection. No REINDEX,
  deletion, deduplication, logout or credential reset is automatic.
- Operational logs omit message bodies, contacts, JIDs, keys and remote error
  text. Explicit terminal pairing still displays a sensitive, short-lived QR
  locally; do not publish that screen or its logs.

Dependency pins were adapted from the three-file upstream PR
[lharries/whatsapp-mcp#355](https://github.com/lharries/whatsapp-mcp/pull/355),
head `1e674b74a23f99361196a1cb64d8ff997a006c4b`. That proposal is not merged
upstream and does not fix the lifecycle defects itself. Base upstream is
`7d6a06dcdce1f01dfb24f60e1030d5efba9f3b88`. There is no build-time `go get @latest`.

## Verify without a real account

Use Go1.26.0 and a C compiler for sqlite3/race tests:

```sh
cd whatsapp-bridge
go mod verify
go test -count=1 -race -timeout=90s ./...
go vet ./...
go build .
```

`BRIDGE_TEST_LEGACY_QR_WAIT=1 go test -run '^TestQRDeadlineRegression$'` is
expected to fail: the test-only legacy implementation ignores its deadline.
The same test without that variable must pass. The production binary never
reads this test selector. CI also checks unlinked startup with networking off
and an empty disposable store, never an actual account or message database.

## Operator gate

Do not point a newly built image at the only copy of a session or message
database. Preserve the originals first. A failed integrity check needs a
separate lossless recovery decision; switching to an empty cache is not
equivalent to recovering history. Do not silently choose conflicting records.

The optional `compose.repair.yaml` uses a different candidate container/port,
requires an explicitly reviewed existing store directory, and does not restart
automatically. It does not replace the deployed container. Real linking and
read-only account validation are manual acceptance steps, not CI checks.

```sh
# After reviewing backups, source identity and the store directory:
export WHATSAPP_STORE_DIR=/absolute/path/to/reviewed-test-store
export WHATSAPP_ALLOW_PAIRING=false
export WHATSAPP_ALLOW_SESSION_UPGRADE=false
docker compose -f compose.repair.yaml build
```

Do not start or set either consent flag to true until the operator has reviewed
the separate pairing and existing-session migration scopes. Keep the
original image/configuration for rollback; never delete a database to obtain
a QR. A remote logout may still cause the upstream SDK's ordinary session
state changes; this patch makes no promise that a live SDK session is immutable.

## Scope

The original send/download HTTP routes remain part of this generic bridge;
it is not itself a read-only MCP. Only bind it to loopback. A separate scoped
read-only adapter must enforce read-only agent access. This patch does not
grant a prospecting agent any new sending permission or human approval.
