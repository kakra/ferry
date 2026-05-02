# Ferry Release Checklist ✅

Use this checklist to verify the system before the first public release (v1.0.0).

## 🛡️ Security
- [x] **Secrets:** Confirm normal startup rejects default secrets and `ferry init-config` generates unique values for `session_secret`, `token_secret`, `static_password`, and `bootstrap_password`.
- [x] **Reverse Proxy:** Verify the supported nginx reverse-proxy path with `behind_reverse_proxy: true`.
- [x] **Rate Limiting:** Confirm that `/login` and `/unlock` are throttled (0.2 req/s).
- [x] **Permissions:** Verify that a user without `can_manage_users` cannot access the user table or API.
- [x] **Self-Protection:** Confirm that an admin cannot disable their own account or remove their own `can_manage_users` right.

## 💾 Data Integrity
- [x] **CAS Stability:** Verify that deduplicated blobs are NOT deleted on upload failure.
- [x] **Orphan Cleanup:** Confirm that the integrity scan ignores files younger than 30m but removes older orphans.
- [x] **TUS Resume:** Verify that partial uploads are preserved for at least 24h.

## 📟 Operations
- [x] **Docker:** Confirm `docker-compose up` starts a healthy system with persistent volumes.
- [ ] **Docker Hub:** Confirm image tags, supported architectures, OCI labels, and push credentials match `docs/RELEASE.md`.
- [ ] **GitHub Pages:** Enable Pages for GitHub Actions and verify the documentation deployment succeeds.
- [x] **Bootstrap:** Verify the `/setup` flow creates the first admin and disables password-only web bootstrap access.
- [x] **Break-Glass:** Confirm the `ferry break-glass` command provides emergency access on `127.0.0.1`.
- [x] **Docs:** Ensure `OPERATIONS.md`, `ARCHITECTURE.md`, and `RELEASE.md` are up to date.
- [x] **Changelog:** Create and verify the release-facing changelog for `v1.0.0`.
- [x] **Archive Cleanup:** Remove internal planning archives from the public release branch after preserving them in a private/local archive or old development branch.

## 🎨 User Experience
- [x] **I18n:** Proofread all German and English translations.
- [x] **Responsive:** Verify the dashboard is usable on a mobile device.
- [x] **Feedback:** Confirm HTMX loading indicators appear for all major actions (Create, Rotate, Cleanup).

## Verification Notes
- Automated verification: `go test ./...`, `go vet ./...`, and `go test -race ./internal/api`.
- Secrets are covered by `internal/config` tests for default rejection and `init-config` generation/repair.
- Rate limiting, permissions, self-protection, bootstrap, break-glass restrictions, CAS stability, orphan cleanup, and TUS retention are covered by API, cleanup, storage, and upload tests.
- Reverse-proxy verification is based on the supported internal nginx path. The public UTM `PATCH`/TUS behavior is tracked as an external proxy issue and is not a Ferry release blocker.
- Docker smoke testing was verified on the deployment host.
- Docker Hub publishing remains open until namespace, tag policy, credentials, and release workflow are finalized.
- `CHANGELOG.md` contains the initial `v1.0.0` release notes.
- Mobile tables currently require horizontal scrolling; this is accepted for `v1.0.0` and tracked as post-release UX polish.
- German and English translations were proofread for release-facing terminology.

---
**Status:** Ready for Release ✅
