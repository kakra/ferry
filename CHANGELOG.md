# Changelog

All notable changes to ferry will be documented in this file starting with the first public release.

The format follows the spirit of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and ferry follows semantic versioning after `v1.0.0`.

## [1.0.0] - 2026-05-02

### Added
- Temporary upload and download shares with generated public tokens and one-time displayed share passwords.
- Password-protected guest access with unlock sessions and password rotation invalidation.
- Resumable uploads using the TUS protocol, including parallel uploads, progress feedback, and upload completion badges.
- Content-addressable storage with SHA-256 based deduplication.
- Automated cleanup worker for expired shares, stale TUS artifacts, and orphaned blobs.
- Local user management with permissions, disabled-user handling, self-protection, and local password authentication.
- First-run `/setup` bootstrap flow and break-glass recovery mode via `ferry break-glass`.
- Admin dashboard with share filtering, sorting, lifecycle indicators, file counts, user management, system status, and log aggregation.
- German and English UI translations with locale-aware date formatting.
- Docker deployment support with BuildKit cache-friendly builds and SQLite WAL-compatible volume layout.
- Operational documentation, architecture notes, release policy, release checklist, and contribution guidelines.

### Security
- Production startup rejects default secrets unless development mode is explicitly enabled.
- `ferry init-config` creates or repairs `config.yaml` with generated secrets.
- Login and guest unlock endpoints are rate limited.
- Sessions use `HttpOnly`, `SameSite=Lax`, and configurable `Secure` cookie attributes for reverse-proxy deployments.
- Share downloads, uploads, and admin operations emit no-store cache headers where appropriate.
- Break-glass mode binds to loopback by default and disables public share access and uploads.

### Known Limitations
- LDAP/AD configuration fields are reserved for a post-v1 release. Enabling LDAP is rejected until the provider is implemented.
- Mobile table layouts may require horizontal scrolling in some admin views.
- Docker Hub publishing automation is not part of this initial release preparation.
