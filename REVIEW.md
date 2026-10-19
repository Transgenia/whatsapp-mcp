# Bridge repair review contract

Review executable behavior, not style. Cite file/line and a reproducer for each
actionable finding. Distinguish introduced, pre-existing and unverified issues.
After a fix, re-review only the changed paths; cap at three rounds before an
operator decision. A reviewer or passing test cannot replace human approval.

Blocking failures:
- A QR, login attempt, reset, logout, deletion or migration is implicit.
- Missing/invalid session or cache becomes a restart loop or apparent success.
- Startup deadline fails to cover QR initialization, connect or event wait.
- Successful startup cancels the SDK session context.
- Logs disclose message content, JIDs, keys, sender names or remote errors.
- Corrupt existing cache is opened writable before validation.
- Send/download handlers accept unauthenticated or logged-out sessions.
- Build changes dependencies via `@latest`, or publishes store/log/credential data.

Checks: Go tests/race/vet, locked-module verification, build and an unlinked
startup smoke test in a network-disabled container with a disposable empty
store. No real WhatsApp account, auth database or message fixture belongs in CI.
Offline success is not proof of a paired session or synchronized history.
