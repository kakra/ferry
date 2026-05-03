# Ferry Security Model 🛡️

This document summarizes implemented security properties, current hardening measures, and the verification practices used for Ferry. It is a technical reference for auditors and security-conscious users.

## 🏛️ Architectural Foundations

### 1. Content-Addressable Storage (CAS)
- **Principle:** Files are stored based on their cryptographic hash (SHA-256).
- **Security Benefit:** Immutable storage. Once a file is written, its location is fixed and tied to its content. This naturally prevents duplicate storage and ensures integrity.

### 2. Single Binary & No Dependencies
- **Principle:** Go + SQLite, zero external services like Redis or S3.
- **Security Benefit:** Minimal attack surface. Fewer moving parts mean fewer integration vulnerabilities and easier auditing of the entire stack.

### 3. ORM-First (Ent)
- **Principle:** All database interactions happen through a typed ORM.
- **Security Benefit:** Reduces SQL injection risk by enforcing typed, parameterized database queries and schema-backed access patterns.

---

## 🛡️ Security Hardening

### 1. Authentication & Session Management
- **Passwords:** All passwords (user and share) are hashed using **Argon2id**, the winner of the Password Hashing Competition.
- **Sessions:** Cookies are configured with `HttpOnly` and `SameSite=Lax`. The `Secure` flag is enabled when ferry is deployed behind a reverse proxy.
- **Rate Limiting:** Built-in protection for `/login` and `/unlock` endpoints (0.2 req/s) to prevent brute-force attacks.
- **Role Separation:** Authentication is independent from management rights. Any active local user may log in; admin and share-management capabilities are granted separately.

### 2. Share Protection
- **Dual-Token Logic:** Links use a random public token, but the server only stores its **hash**. Even a database leak doesn't reveal valid share links.
- **One-Time Passwords:** Share passwords are shown only once upon creation.
- **Session Isolation:** Guests can only delete files they uploaded within their current session, unless the admin explicitly permits otherwise.

### 3. Input Validation & Sanitization
- **Open Redirect Protection:** `next` parameters in the login flow are sanitized so redirects stay local.
- **XSS Prevention:** Leverages Go's `html/template` package, which provides context-aware auto-escaping.
- **Path Traversal:** File paths are constructed using hashes and internal IDs, never directly from user-supplied filenames.
- **Configuration Injection Protection:** `ui.primary_color` is validated as a hex color code before it is accepted from configuration.

### 4. Information Leak Prevention
- **Success ID Validation:** The UI logic for showing "Upload Success" badges validates every ID against the currently active share. This prevents probing for the existence of files in other shares.
- **Conservative Error Handling:** User-facing errors stay concise for expected validation and permission failures; detailed operational diagnostics remain in logs and test output.

---

## 🧹 Automated Maintenance
- **Mark-and-Sweep GC:** A background worker automatically deletes expired shares, incomplete TUS uploads, and orphaned blobs, adhering to the principle of **Data Minimization**.

## 🎯 Threat Model
- **Designed For:** Trusted self-hosted deployments, temporary file exchange, and small operational teams that control their own infrastructure.
- **Not Designed For:** Hostile multi-tenant hosting, zero-trust public exposure, or long-term confidential storage.
- **Assumed Trust Boundary:** The operating system, local filesystem, and deployment operator are part of the trust boundary.

## ⚠️ Known Limitations
- **No application-level encryption at rest:** ferry relies on the underlying host or storage layer for disk encryption.
- **No central session revocation:** Sessions are cookie-based, so active sessions cannot be centrally enumerated or invalidated without replacing the session store.
- **No distributed deployment model:** ferry is intentionally single-node and SQLite-backed.
- **No complete quota enforcement layer yet:** Size and retention limits are configured, but they are not a substitute for external storage controls.
- **No remote break-glass administration:** Recovery mode is intentionally local-only and must be started explicitly.
- **Browser assets are still a supply-chain concern:** ferry must eventually vendor browser-side libraries such as `htmx` and `tus-js-client`, verify them against an authoritative manifest, and embed them into the Go binary so production builds no longer depend on runtime downloads from public CDNs. Branding URLs remain allowed because they are treated as operator-controlled first-party content, not third-party delivery networks.

## 🌐 Planned Browser Asset Hardening
- **Planned Breaking Change:** This is intended as a future release change, likely in a 1.x hardening release or in v2. It will replace configurable browser asset URLs with convention-based, operator-provided files.
- **Vendored Assets:** Browser assets such as `htmx` and `tus-js-client` should live in the repository and become part of the release artifact.
- **Manifest-Driven Verification:** A manifest must declare asset name, version, upstream URL, license, local path, and SHA256 checksum.
- **Build Guarantees:** Normal builds must never download browser assets from the internet. Builds must continue to work even if upstream URLs disappear.
- **CI Enforcement:** Local vendored assets must be checked against manifest checksums during build, test, and CI; any missing asset or checksum mismatch must fail validation.
- **Binary Packaging:** Vendored browser assets should be embedded into the Go binary via `go:embed`.
- **Update Workflow:** A developer-only helper under `tools/` may rewrite vendored assets and refresh the manifest. Updating an asset must commit both the file change and the manifest change.
- **Build Pipeline Scope:** This policy must not introduce Node.js/npm or a `hack/` directory.
- **CSP Impact:** Runtime loading from public CDNs should no longer be required once the vendored assets are in place.

## 🚀 Development Process
Ferry is built using a **Multi-AI Orchestration** approach:
- **Google Gemini:** High-level logic, UI polish, and architectural guidance.
- **OpenAI Codex:** Implementation, code reviews, and deep-dive debugging.
- **Human Oversight:** Kai Krakow acts as the "Conductor," defining goals, validating every phase, and performing final quality assurance.

## 🔍 Verification
- GitHub CodeQL is enabled for the repository and is used to catch security regressions in pull requests.
- The local release checklist requires `go test ./...`, `go vet ./...`, and `go test -race ./internal/api`.
- Ad hoc audit sweeps are used during development to check for regressions such as cross-share `success_ids` leakage and configuration injection hazards.
