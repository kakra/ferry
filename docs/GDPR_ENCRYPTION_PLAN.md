# Encryption at Rest & GDPR Compliance Plan 🛡️⚖️

This document outlines the architectural considerations and implementation strategy for bringing technical data protection and GDPR ("DSGVO") compliance to Ferry.

## 🎯 Primary Goals
1.  **Technical Sovereignty:** Ensure that data stored on third-party infrastructure (e.g., German Docker hosters) is unreadable without the local `master_encryption_key`.
2.  **Compliance:** Provide built-in tools for IP anonymization and data minimization.
3.  **CAS Integrity:** Implement encryption in a way that does **not** break Content-Addressable Storage (deduplication).

---

## 🔐 Encryption at Rest (EaR)

### The CAS Challenge
Traditional encryption uses random Nonces/IVs, resulting in different ciphertexts for the same plaintext. This would break deduplication. To solve this, Ferry will use **Convergent Encryption**.

### Technical Specification
-   **Algorithm:** XChaCha20-Poly1305 (High performance, large nonce space).
-   **Key Management:** A system-wide `master_encryption_key` (32 bytes, Base64) stored in `config.yaml`.
-   **Nonce Generation:** The Nonce is derived from a HMAC of the file's SHA-256 hash using the master key.
    -   `Nonce = HMAC-SHA256(MasterKey, FileHash)[:24]`
    -   This ensures that the same file encrypted with the same MasterKey always yields the same ciphertext, allowing deduplication to function perfectly while remaining secure against anyone who doesn't possess the MasterKey.

### Migration Strategies

#### Strategy 1: Soft Migration (Lazy)
-   **Method:** The storage layer becomes "encryption-aware." It attempts to read blobs as encrypted; if decryption fails or a version flag is missing, it falls back to plain reads.
-   **Writing:** All new uploads are encrypted immediately.
-   **Pros:** Zero downtime, no immediate CPU spike.
-   **Cons:** Mixed state (encrypted/unencrypted) on disk for a long time.

#### Strategy 2: Hard Migration (Batch)
-   **Method:** A dedicated CLI command `ferry storage encrypt` walks the entire CAS and encrypts every blob in one go.
-   -   **Pros:** Clean state, maximum security once finished.
-   **Cons:** Requires maintenance window, high I/O and CPU load during migration.

---

## 👤 GDPR ("DSGVO") Features

### IP Anonymization
-   **Requirement:** IP addresses are PII. Storing them in logs is problematic.
-   **Feature:** `server.anonymize_ips: true`.
-   **Implementation:** Before writing to the `MemoryLogger` or standard output, the last octet (IPv4) or last 80 bits (IPv6) are masked (e.g., `192.168.1.XXX`).

### Database Field Encryption
-   **Requirement:** Meta-data (filenames, notes) are stored in plain text in the SQLite database.
-   **Feature:** Encrypt specific columns using the system master key.
-   **Affected Fields:** `File.original_name`, `Share.note`, `User.display_name`.

---

## 🚀 Target Release: v2.0
Due to the significant impact on the storage layer and database schema, this feature set is targeted for a **v2.0 release**.

### Planned Milestones
1.  **v1.x:** Introduce `master_encryption_key` in `init-config` (reserved for future use).
2.  **v2.0-beta:** Implement Convergent Encryption in `FileStorage` with lazy-read support.
3.  **v2.0-final:** Add IP anonymization and CLI migration tools.
