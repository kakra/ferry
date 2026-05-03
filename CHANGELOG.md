# Changelog

All notable changes to ferry will be documented in this file starting with the first public release.

The format follows the spirit of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and ferry follows semantic versioning after `v1.0.0`.

## [1.0.4] - 2026-05-03

### Added
- **Global Security Headers:** Every response now includes `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and a strict `Content-Security-Policy`.
- **Differentiated User Feedback:** The dashboard now displays whether you are logged in as an admin or a standard user, including your display name.
- **Enhanced Security Documentation:** Updated [Security Model](docs/SECURITY_MODEL.md) to reflect the new hardening measures.

### Fixed
- **Multi-User Isolation:** Standard users now only see and manage their own shares on the dashboard.
- **Directional Semantics:** Restored the intended upload logic: Guests upload to 'Receive' (Upload) shares, while Owners/Admins upload to 'Send' (Download) shares for management.
- **TUS Authorization:** Every TUS upload request (`POST`, `PATCH`, `HEAD`) is validated against the current session to ensure the user has access to the associated share.
- **Badge Persistence:** Improved the 'Upload Success' badge tracking to survive HTMX list refreshes correctly.
- **UI Refinements:** Restored the 'Knight Rider' processing glint animation and fixed a broken navigation tag.
- **Permissions Regression:** Authenticated admins can now correctly delete shares in break-glass mode again.

### Security
- **Restricted Permissions:** Storage and database directories are now created with `0700` permissions. Blobs and database files are restricted to `0600`.
- **Setup Protection:** The `/setup` bootstrap flow is now protected by the authentication rate-limiter.

## [1.0.3] - 2026-05-02

### Added
- Author and link [Security Model](docs/SECURITY_MODEL.md) to transparently explain ferry's hardening and architectural defense measures.

### Security
- Hardened `success_ids` tracking logic by validating ID ownership against the active share, preventing side-channel information leaks.
- Enforced strict hex-color validation for `ui.primary_color` in the configuration to prevent potential CSS injection vectors.

## [1.0.2] - 2026-05-02

### Added
- Comprehensive branding support: Customize the application name, browser icon (favicon), and footer text via `config.yaml`.
- Refined UI styles and layouts for better accessibility and a more professional look.
- Better Dark Mode support for tables and the theme switcher.

## [1.0.1] - 2026-05-02

### Fixed
- Fixed several Open Redirect vulnerabilities in the login handler (CVE-style security fixes).
- Fixed image path issues in the documentation deployment for GitHub Pages.
- Updated documentation deployment trigger to correctly target the `main` branch.

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
