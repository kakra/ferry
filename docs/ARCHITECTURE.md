# ARCHITECTURE.md

## 🧭 Design Principles

- **Convention over Configuration:** Sensible defaults, minimal settings.
- **Single-Binary Operations:** Go + SQLite. No external dependencies (Redis, S3, etc.).
- **SQLite WAL Mode:** Mandatory Write-Ahead Logging (WAL) for improved concurrency and filesystem friendliness. Only local filesystems are supported (no NFS/SMB).
- **Deduplication by Default:** Mandatory CAS (Content-Addressable Storage).
- **Robust Simplicity:** Mark-and-sweep GC for resilience.
- **Low Maintenance:** Designed for small IT teams. SQLite is the source of truth.
- **Stateless Frontend:** Designed to run behind Reverse Proxies.
- **Stable Releases:** Breaking changes are reserved for major releases. Feature flags are avoided unless required for safety, migration, or operational compatibility.

## 🗄️ Database & Concurrency (SQLite)

ferry uses SQLite in **WAL mode** with the following mandatory settings:
- `PRAGMA journal_mode=WAL;`
- `PRAGMA foreign_keys=ON;`
- `PRAGMA busy_timeout=5000;`

### 💾 Storage Requirements
- The database file MUST be stored on a **local filesystem**.
- Network filesystems (NFS, SMB, etc.) are NOT supported for the database file as they can break WAL locking and lead to corruption.

### 🛡️ Backup & Maintenance
SQLite WAL creates additional `*.wal` and `*.shm` files. A simple file-level copy while ferry is running is NOT sufficient for a consistent backup.
**Recommended backup methods:**
1. Stop ferry before copying files.
2. Use `VACUUM INTO 'backup.db'`.
3. Use the CLI: `sqlite3 ferry.db ".backup 'backup.db'"`

## 🗄️ Data Model

### Blobs (Storage)
- `hash` (PK): SHA-256
- `size`: Bytes
- `storage_path`: Path on disk
- `unreachable_since`: Timestamp (null if active)
- `created_at`: Timestamp

### Files (Logical)
- `id` (PK): UUID
- `blob_hash`: Link to Blob
- `share_id`: Link to Share
- `original_name`: Original filename
- `upload_session_id`: UUID (Optional, used for guest deletion)

### Shares
- `id` (PK): UUID (Internal identifier)
- `owner_id`: Link to User
- `type`: `download` (standard) or `upload` (reverse share/dropbox)
- `token_hash`: HMAC-SHA256 of the access token
- `password_hash`: Argon2id hash of the share password
- `title`: String (Mandatory, for usability)
- `note`: String (Optional, for guest instructions)
- `expires_at`: Timestamp

### Users
- `id` (PK): UUID
- `username`: String (Unique; local users use `USERNAME`, external identities use `USER@REALM`)
- `auth_source`: Enum (`local`, `ldap`)
- `auth_realm`: String (Optional; set for external identities)
- `display_name`: String
- `password_hash`: String (Optional, only for non-LDAP users)
- `can_manage_all_shares`: Boolean (View/Delete all shares)
- `can_manage_users`: Boolean (Edit other users/permissions)
- `disabled`: Boolean (Local override, prevents login)
- `created_at`: Timestamp

## 🧹 Internal Cleanup Worker (Mark-and-Sweep)

The **cleanup worker is fully implemented** and manages the lifecycle of physical data:
1. **Automated:** Runs every 15m by default.
2. **Triggerable:** Functional via Admin UI or `ferry cleanup` CLI command.
3. **Internal API:** CLI trigger uses a protected HTTP API (`/api/admin/cleanup`) with the static password as Bearer token.
4. **Mark:** Reachable blobs are defined as hashes linked by active, non-expired shares. Unreferenced blobs older than 30m (grace period) are marked with `unreachable_since`.
5. **Sweep:** Blobs marked as unreachable for more than 24h are physically deleted from disk and DB.

## ⚠️ Transactional Safety & Race Conditions

- **Rule:** A `blob` record and its corresponding `file` record must be created within the same database transaction.
- **GC Protection:** The GC ignores any blobs created within the last 30 minutes (grace period).

## 🔐 Security Model

### Authority & Authentication
- **Authority:** Local SQLite users are the source of truth for permissions, ownership, and disabled state.
- **Local Users Always Exist:** Every authenticated identity is represented as a local SQLite user. Planned external providers such as LDAP/AD will provision or update local user records instead of becoming an authorization backend.
- **Login Policy:** Any active local user may log in if the password is valid. Global management rights are not required for authentication.
- **LDAP:** LDAP/AD is planned as an authentication provider only. Local `disabled` state and local permissions remain authoritative.
- **No Public Sign-up:** User management is strictly internal.

### Role Model
- **Admin:** Platform-level role for user administration and server internals. Admins can manage users, inspect status and logs, and perform recovery-oriented maintenance.
- **Manager:** Share-management role. Managers can see and manage shares according to their granted scope.
- **Owner:** A per-share relation, not a global role. The owner of a share automatically acts as that share's manager.
- **Orthogonality:** Admin and Manager are separate capabilities. A user may have one, both, or neither.
- **Current Product Rule:** Normal authenticated users may still manage their own shares through ownership, even when they do not hold global management permissions.

### Identity Model
- **Local Accounts:** Local users authenticate with a locally managed password hash and must not contain `@` in their username.
- **External Accounts:** Planned external identities are stored canonically as `USER@REALM`.
- **Auth Source Binding:** A local account must not fall back to an external provider, and an external account must not fall back to a local password.
- **Multi-Realm Ready:** The `USER@REALM` format keeps the model open for multiple external realms without changing identifiers.

### LDAP/AD Planning
- **Implementation Status:** LDAP/AD is planned after v1.0.0. Configuration fields are reserved, but `ldap.enabled=true` is rejected until the provider is implemented.
- **Primary Target:** Windows Active Directory is the primary target for the first implementation.
- **Secondary Goal:** The configuration model should remain flexible enough for other LDAP implementations through search filters and field mapping.
- **Authorization Boundary:** LDAP will not be the source of permissions. Group mapping is intentionally out of scope for the first LDAP release.
- **Provisioning Rule:** A successful first-time LDAP login should provision or update a local user record, after which local permissions and ownership apply.
- **Availability Rule:** If an LDAP-backed identity cannot be authenticated against its upstream directory, login is denied.

### Transport Security For LDAP
- **Supported Modes:** `ldaps://` and `ldap://` with StartTLS.
- **Secure By Default:** Certificate verification must be enabled by default.
- **Trusted CA Options:**
    1. Use the system trust store.
    2. Load an explicitly configured CA certificate bundle.
    3. Allow an insecure bypass mode only as an explicit unsafe development or test override.
- **No Primary Fingerprint Pinning Requirement:** CA trust is the preferred operational model. Fingerprint pinning may be added later if operationally useful.

### Session Management
- **Current:** Signed client-side sessions via `gorilla/sessions` cookie store. Active sessions cannot be centrally listed or revoked.
- **Future:** DB-backed session storage may be added if centralized session management becomes necessary.
- **Guest Unlock Revocation:** Changing a share password must invalidate all existing guest unlock sessions for that share. This should be enforced with a server-validated share-specific unlock version or equivalent nonce/timestamp, not by trusting a bare boolean in the cookie alone.

### Multi-User Permissions
- `can_manage_all_shares`: Global share-management rights.
- `can_manage_users`: Platform-admin rights for managing other users and system permissions.
- **Ownership Override:** Share ownership grants management rights for that specific share even without global `can_manage_all_shares`.
- **Self-Protection:** Admin users cannot delete or disable themselves, or remove their own last user-management permission.

### Bootstrap & Administration
- **Initial Setup:** `/setup` is available only while no local user exists.
- **Setup Secret:** `bootstrap_password` authorizes creating the first local administrator.
- **Web Login:** The web UI accepts local user credentials only. The static maintenance password is not accepted by the web login.
- **CLI / Admin API Auth:** The static password remains active as a Bearer token for CLI-triggered maintenance tasks such as cleanup until a dedicated API-token model exists.
- **Release Rule:** Public releases must describe supported upgrade paths only.
- **Release Rule:** Starting with the first public release, a maintained changelog is required.
- **Release Rule:** Long-lived feature gates are not part of the normal development model. Deprecation flags may prepare the next major release, but must be removed during that major-release cleanup.

### `/create` Automation Deep-Link
- **Purpose:** Fast-track share creation for external systems (e.g., Ticket-Systems, CRM, Scripts).
- **Authentication:** Requires an authenticated user with share-management rights.
- **Query Parameters:**
    - `title`: Pre-fills the share title.
    - `note`: Pre-fills the share note/message.
- **Behavior:** Renders a focused share creation form with pre-filled fields. Submitting the form creates a normal share owned by the authenticated user.

### Admin `id:<uuid>` Preparation Path
- **Purpose:** Allows administrators to reopen and prepare a share (add/remove files) without possessing the public token.
- **Access:** Only valid for active administrator sessions.
- **URL Pattern:** `/admin/shares/:id/prepare` (internal redirect to `/s/id:<uuid>`).
- **Security:** The `id:` prefix is a reserved internal token format. It is never valid for public guest access and is strictly bound to admin authorization.

### Share Direction Semantics
- **Sending Share:** The share is prepared by the owner or manager and sent outward to a recipient. The recipient mostly downloads, while the owner may upload content during preparation.
- **Receiving Share:** The share is opened for inbound collection. The recipient mostly uploads, while the owner later reviews and downloads the received content.
- **Important:** Both share types can involve uploads and downloads. The type describes the expected workflow direction, not an exclusive protocol restriction.
- **Terminology Rule:** User-facing labels should describe the workflow direction itself, not the guest-side action. Avoid wording that would reintroduce the old `Upload-Share` / `Download-Share` ambiguity.

### System Monitoring & Status (`/status`)
- **Metrics Scope:**
    - **Logical Volume:** Sum of the size of all file records (includes duplicates).
    - **Physical Usage:** Actual disk space used by unique blobs in CAS (excludes marked-for-deletion blobs).
    - **Deduplication Ratio:** Efficiency of the CAS storage.
    - **Temp Usage:** Real-time disk usage of the `tmp/` directory.
- **Operational Guarantees:** The status page provides operational observability but is not an exhaustive forensic audit tool. It relies on database state and basic filesystem stats.

### Storage Integrity & Cleanup Behavior
- **TUS Artifacts:** Files in `tmp/` are kept for a configurable duration (default 24h) to support TUS resumes. They are swept by the background worker after expiration.
- **CAS Integrity Scan:** The background worker identifies physical files in `storage/` that lack a corresponding database record (orphans).
- **Finalization Race Protection:** Physical files younger than 30 minutes (grace period) are ignored by the integrity scan to prevent race conditions during upload finalization.
- **Safe Error Compensation:** If a database transaction fails during upload finalization, the system attempts to delete the orphaned physical file *only if* it was newly created during that run. Existing shared blobs are preserved.

### Login Rules
- **LDAP Username Form:** `USER@REALM`
- **Local Login:** Only allowed for `auth_source=local` and requires a valid local password hash.
- **LDAP Login:** Planned external login is only allowed for `auth_source=ldap` or a first-time LDAP provisioning flow.
- **Deny Conditions:**
    - local `disabled=true`
    - LDAP authentication failure
    - LDAP account disabled or otherwise rejected by directory policy when such state is detectable
- **Administrative Independence:** A local app admin can disable a user even if the LDAP directory would otherwise authenticate them successfully.

### Break-Glass Recovery Mode
- **Command:** `ferry break-glass`
- **Purpose:** Explicit emergency recovery mode for repairing access, user state, or configuration when normal administration is unavailable.
- **Activation:** Never automatic. Operators must stop the normal service and start the server manually in break-glass mode.
- **Independence From Current User State:** Break-glass is available regardless of whether local users already exist.
- **Authentication:** Break-glass does not re-enable the old static admin password flow. It uses an explicit recovery/bootstrap secret.
- **Operational Intention:** The mode must be useful for recovery, but intentionally inconvenient for normal production use.
- **Binding Rule:** `ferry break-glass` must bind to loopback by default and must not reuse the normal public listener.
- **Default Listener Shape:** Use `127.0.0.1` with a dedicated recovery port.
- **Exposure Rule:** External exposure of break-glass is not supported for normal operation.
- **Future Override Rule:** If a bind override is ever added, it must be explicit, highly visible in logs/UI/docs, and documented as unsafe.
- **Allowed Operations:** Repair or create local admin access, enable or disable local users, set or reset local passwords, inspect user and share state, delete shares when necessary for recovery or cleanup, and inspect essential system status.
- **Blocked Operations:** Public share access, guest unlock flows, regular file downloads for end users, uploads/TUS, and normal share creation.
- **Operator Feedback:** The UI and logs must clearly indicate that break-glass mode is active.
- **Security Goal:** Break-glass must be a recovery workflow, not an alternative long-term operating mode.

## 📟 CLI

- `ferry serve`: Run the web server and background worker.
- `ferry cleanup`: Trigger the internal worker via API.
- `ferry migrate`: Run database schema migrations.
- `ferry break-glass`: Start the restricted recovery mode for administrative repair.

## 🔐 Encryption Model

ferry does not implement application-level encryption-at-rest.

### Rationale

- ferry uses mandatory Content-Addressable Storage (CAS).
- CAS relies on deterministic file content (SHA-256 hashing).
- Encrypting files at the application layer would break deduplication, because identical files would produce different ciphertexts.
- This design prioritizes simplicity, reliability, and operational transparency over cryptographic storage isolation.

### Security Model

- Access control is enforced via:
    - share tokens (possession factor)
    - share passwords (knowledge factor)
- ferry is intended to run in trusted environments:
    - self-hosted servers
    - internal infrastructure
- Data at rest protection is expected to be handled by the underlying system:
    - full disk encryption (LUKS, BitLocker, ZFS encryption)
    - encrypted virtual machines
    - secure storage backends

### Non-Goals

- No application-level encryption-at-rest
- No end-to-end encryption
- No client-side encryption

### Implications

- Files are stored in plaintext on disk.
- Anyone with direct filesystem access can read stored files.
- This is considered acceptable for the intended use case (temporary file exchange in controlled environments).
