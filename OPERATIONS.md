# Isolated pairing and partial-cache validation

This procedure uses a separate local store and an explicitly selected image.
It does not replace an existing bridge, repair history, send messages, or grant
an agent access to the bridge's generic send/download API. Keep original stores,
containers and configurations unchanged until a separate migration is reviewed.

A real pilot successfully paired and later passed an offline integrity check.
It also produced six write warnings. Subsequent successful writes and a clean
integrity check do **not** prove that those writes were recovered or that the
history is complete. Treat the cache as partial. The underlying SQLite errors
were not recorded, so their cause remains unverified.

## Before the first pairing

Use a reviewed local image whose source commit and build evidence are known.
Do not substitute a mutable tag, pull a replacement automatically, or build an
unreviewed image during pairing. Have the account owner's phone available.

The commands below are **PowerShell**, run from this repository. If the prompt
is CMD (`C:\...>`), enter `powershell.exe -NoProfile` first. The Compose file is
portable; adjust shell syntax on other operating systems.

Set the image and an absolute, local, **new** store path. The chosen directory
must be outside any publishable source tree or explicitly ignored by Git.
Do not select the original store, a backup of it, or a shared/network directory.

```powershell
$ErrorActionPreference = 'Stop'
$env:WHATSAPP_PILOT_IMAGE = (Read-Host 'Reviewed local image ID, sha256 followed by 64 hex digits').Trim()
if ($env:WHATSAPP_PILOT_IMAGE -notmatch '^sha256:[0-9a-f]{64}$') {
    throw 'Use the exact reviewed local image ID.'
}
$ActualImage = & docker image inspect --format '{{.Id}}' $env:WHATSAPP_PILOT_IMAGE
if ($LASTEXITCODE -ne 0 -or $ActualImage.Trim() -ne $env:WHATSAPP_PILOT_IMAGE) {
    throw 'The reviewed image is not available locally. Stop and review.'
}

$PilotStoreInput = Read-Host 'Absolute path for a new local pilot store'
if ($PilotStoreInput -notmatch '^[A-Za-z]:[\\/]') { throw 'Use an absolute local drive path.' }
$PilotStore = [IO.Path]::GetFullPath($PilotStoreInput)
if (Test-Path -LiteralPath $PilotStore) { throw 'Store already exists. Do not overwrite or reset it.' }
$env:WHATSAPP_PILOT_STORE_DIR = $PilotStore

# An explicitly empty file prevents Compose from discovering a default .env.
# This file contains no credentials and is unrelated to any existing .env file.
$EmptyVars = Join-Path (Get-Location).Path 'compose-vars.empty'
if (-not (Test-Path -LiteralPath $EmptyVars)) {
    [IO.File]::WriteAllText($EmptyVars, '')
}
if ((Get-Item -LiteralPath $EmptyVars).Length -ne 0) { throw 'compose-vars.empty must be empty.' }

$ComposeFile = Join-Path (Get-Location).Path 'compose.pilot.yaml'
$ComposeArgs = @('compose', '--env-file', $EmptyVars, '-p', 'whatsapp-bridge-pilot', '-f', $ComposeFile)
$PilotContainer = 'whatsapp-bridge-pilot-initial'
$ContainerNames = @(& docker ps -a --format '{{.Names}}')
if ($LASTEXITCODE -ne 0) { throw 'Docker is unavailable.' }
if ($ContainerNames -contains $PilotContainer) { throw 'Initial pilot already exists. Review it; do not recreate it.' }

$env:WHATSAPP_ALLOW_PAIRING = 'true'
$env:WHATSAPP_ALLOW_SESSION_UPGRADE = 'false'
& docker @ComposeArgs config --quiet
if ($LASTEXITCODE -ne 0) { throw 'Compose validation failed.' }
New-Item -ItemType Directory -Path $PilotStore -ErrorAction Stop | Out-Null
try { & docker @ComposeArgs run --no-deps --pull never --name $PilotContainer bridge }
finally {
    $env:WHATSAPP_ALLOW_PAIRING = 'false'
    $env:WHATSAPP_ALLOW_SESSION_UPGRADE = 'false'
}
```

The foreground terminal shows the QR. In the phone app, open **Linked devices >
Link a device** and scan it locally. Do not share the QR or capture it in logs,
screenshots, support tickets, or CI artifacts. The logging driver is `none`, so
`docker logs` will not recover this terminal output later.

The bridge's startup deadline is three minutes. Look for
`bridge_state=authenticated`; `transport_event=connected` alone is not enough.
Keep the terminal open while synchronization proceeds. A running container is
not proof of synchronized history. On `reason=...`, preserve the store and report
only the fixed diagnostic code. Do not delete the store to get another QR.

The service declares no ports, and the command does not use `--service-ports`
or `--publish`. It has network access to WhatsApp for pairing/synchronization,
but does not publish the REST API on the host. No other service is started.

## Stop without discarding the linked session

Use a second PowerShell window, or `Ctrl+C` in the foreground window:

```powershell
docker stop --timeout 30 whatsapp-bridge-pilot-initial
docker inspect --format '{{.State.Running}} {{.State.ExitCode}}' whatsapp-bridge-pilot-initial
```

Require `false 0` before the offline checks below. A graceful stop disconnects
the process and preserves the store; it does not request account logout. If the
exit is unclean, preserve the files for review. Do not delete SQLite sidecars,
run REINDEX/VACUUM, choose duplicate rows, or reset authentication.

## Offline, read-only cache checks

Stop **every process that can write this pilot store** first. This is especially
important when a Linux container writes a Windows bind mount: do not inspect it
simultaneously using a Windows SQLite client. Do not open `whatsapp.db` for these
checks; only `messages.db` is in scope.

The following optional checks require an already-installed SQLite CLI. If it is
unavailable, stop and use a separately reviewed diagnostic tool. These commands
neither install tools nor copy or repair a database. Run them in the PowerShell
session where `$PilotStore` and `$EmptyVars` were set, after confirming the exact
store and the cleanly stopped writer.

```powershell
Get-Command sqlite3 -ErrorAction Stop | Out-Null
$Cache = Join-Path $PilotStore 'messages.db'
if (-not (Test-Path -LiteralPath $Cache -PathType Leaf)) { throw 'Message cache is missing.' }
foreach ($Suffix in @('-wal', '-shm', '-journal')) {
    if (Test-Path -LiteralPath ($Cache + $Suffix)) { throw 'SQLite sidecar exists. Preserve it and review.' }
}

# Reject WAL format before SQLite can open or create sidecars.
$Header = New-Object byte[] 100
$CacheStream = [IO.File]::Open($Cache, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
try { $HeaderLength = $CacheStream.Read($Header, 0, $Header.Length) }
finally { $CacheStream.Dispose() }
if ($HeaderLength -ne 100 -or [Text.Encoding]::ASCII.GetString($Header, 0, 15) -ne 'SQLite format 3' -or $Header[15] -ne 0 -or $Header[18] -ne 1 -or $Header[19] -ne 1) {
    throw 'Unexpected SQLite format. Preserve the source and review.'
}

$BeforeHash = (Get-FileHash -LiteralPath $Cache -Algorithm SHA256).Hash
$CheckSql = @'
PRAGMA query_only=ON;
PRAGMA trusted_schema=OFF;
BEGIN;
SELECT CASE WHEN COUNT(*)=1 AND MIN(integrity_check)='ok' THEN 'INTEGRITY_OK' ELSE 'INTEGRITY_HOLD' END FROM pragma_integrity_check;
SELECT CASE WHEN EXISTS(SELECT 1 FROM pragma_foreign_key_check) THEN 'FOREIGN_KEY_HOLD' ELSE 'FOREIGN_KEYS_OK' END;
SELECT CASE WHEN EXISTS(SELECT 1 FROM messages GROUP BY id, chat_jid HAVING COUNT(*) > 1) THEN 'DUPLICATE_KEYS_HOLD' ELSE 'DUPLICATE_KEYS_OK' END;
COMMIT;
'@
# The empty init file also avoids loading a user's default SQLite startup file.
$Results = @(& sqlite3 -batch -readonly -init $EmptyVars -cmd '.bail on' -cmd '.timeout 2000' $Cache $CheckSql)
if ($LASTEXITCODE -ne 0) { throw 'SQLite check failed. Review locally; do not publish raw diagnostics.' }
$AfterHash = (Get-FileHash -LiteralPath $Cache -Algorithm SHA256).Hash
if ($BeforeHash -ne $AfterHash) { throw 'Cache changed during verification. Stop and review writers.' }
if (($Results -join ',') -ne 'INTEGRITY_OK,FOREIGN_KEYS_OK,DUPLICATE_KEYS_OK') {
    throw 'Offline checks did not all pass. Preserve the source.'
}
$Results
```

Only fixed status strings are printed. Do not export message rows, contact names,
phone numbers, media keys, database files, authentication files, or their hashes
to public issue trackers. A successful check establishes consistency of the
available cache at that time. It cannot establish delivery completeness, identity,
consent, or recovery of previous write failures. This example is not an attestation
manifest for any specific downstream reader.

## Reopen the same reviewed pilot session

This is a separate operation from first pairing. Opening an existing authentication
store can trigger schema migrations in the SDK. Obtain operator authorization
for that **exact store and image** before enabling the session-upgrade permission.
Review backup/rollback arrangements; never use the only original store as a test.

Keep all previous writers stopped. After authorization and a fresh cache check,
reuse the same reviewed `$PilotStore`, image, `$ComposeArgs`, and empty variable
file. Change only the two explicit flags and choose a new container name:

```powershell
$env:WHATSAPP_ALLOW_PAIRING = 'false'
$env:WHATSAPP_ALLOW_SESSION_UPGRADE = 'true'
$ResumeContainer = 'whatsapp-bridge-pilot-resumed'
$ContainerNames = @(& docker ps -a --format '{{.Names}}')
if ($LASTEXITCODE -ne 0) { throw 'Docker is unavailable.' }
if ($ContainerNames -contains $ResumeContainer) { throw 'Reviewed resume container already exists.' }
& docker @ComposeArgs config --quiet
if ($LASTEXITCODE -ne 0) { throw 'Compose validation failed.' }
try { & docker @ComposeArgs run --no-deps --pull never --name $ResumeContainer bridge }
finally {
    $env:WHATSAPP_ALLOW_PAIRING = 'false'
    $env:WHATSAPP_ALLOW_SESSION_UPGRADE = 'false'
}
```

This preserves the first container as history and does not force a fresh QR.
If the saved session is unavailable, the bridge holds with pairing disabled.
Do not silently enable pairing, remove a database, or migrate the original store.
`docker start` on the initial container does not adopt new environment flags and
will normally stop at the existing-session consent gate.

## Downstream read-only integration

Keep sending permissions separate. Integrate only a scoped read-only adapter,
not the generic REST send/download endpoints. Mount only the reviewed message
cache and any required minimized attestation, never the authentication database
or the whole store directory. An offline snapshot can remain readable while the
bridge is stopped; it is not a live feed.

If a reader verifies exact cache bytes, renewed synchronization changes those
bytes and invalidates its prior attestation. Revalidate according to that
reader's contract. Keep `history_complete=false`, require human review of
ambiguous coverage, and do not infer absent conversations or consent from a
partial cache. Activating a reader does not activate a prospecting schedule or
authorize a campaign.
