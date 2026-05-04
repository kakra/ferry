# ferry Roadmap

This roadmap lists supported v1 capabilities and planned work after the first public release. It is intentionally release-facing and omits internal development milestones.

## v1.0.0 Scope
- SQLite-backed local users, admin/manager roles, and per-share ownership.
- Password-protected send and receive shares with expiring public links.
- TUS-based resumable uploads with content-addressable storage and deduplication.
- Mark-and-sweep cleanup for expired shares, incomplete uploads, and unreachable blobs.
- Initial setup via `/setup` and explicit local recovery via `ferry break-glass`.
- German and English UI translations.
- Docker-oriented deployment with reverse-proxy support.

## Planned After v1.0.0
- LDAP/Active Directory authentication provider.
- Canonical external identities in `USER@REALM` form.
- LDAP auto-provisioning into local user records while keeping permissions in SQLite.
- Optional LDAP field mapping for non-AD directories.
- Planned breaking browser asset hardening for a future 1.x or v2 release: replace configurable browser asset URLs with convention-based, operator-provided files; vendor `htmx` and `tus-js-client` with manifest checksums, `go:embed` packaging, and a developer-only update helper under `tools/`.
- DB-backed session storage if centralized session listing or revocation becomes necessary.
- Narrow mobile table refinements to reduce horizontal scrolling.
- Enforced file, share, and storage quota limits.
- Dedicated API-token model for maintenance automation.
- A future major release should rename the current guest-facing share types to workflow-oriented terminology such as `Send Share` and `Receive Share`, and update the code, UI, and documentation accordingly.

## Planned for v2.0 (Enterprise & GDPR)
- **Encryption at Rest (EaR):** Convergent encryption for CAS blobs using a system master key.
- **GDPR Compliance:** IP anonymization for logs and database field encryption.
- **Migration Tools:** CLI tools for bulk-encrypting existing storage.
- **Container Hardening:** Run the Docker image as a dedicated non-root user, with a documented ownership migration strategy for mounted config, database, and storage volumes.
- See [GDPR_ENCRYPTION_PLAN.md](GDPR_ENCRYPTION_PLAN.md) for details.
