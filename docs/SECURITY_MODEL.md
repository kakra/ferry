# Ferry Security Model 🛡️

This document explains the security principles, architectural choices, and hardening measures that make Ferry a robust tool for secure file exchange. It serves as a technical reference for auditors and security-conscious users.

## 🏛️ Architectural Foundations

### 1. Content-Addressable Storage (CAS)
- **Principle:** Files are stored based on their cryptographic hash (SHA-256).
- **Security Benefit:** Immutable storage. Once a file is written, its location is fixed and tied to its content. This naturally prevents duplicate storage and ensures integrity.

### 2. Single Binary & No Dependencies
- **Principle:** Go + SQLite, zero external services like Redis or S3.
- **Security Benefit:** Minimal attack surface. Fewer moving parts mean fewer integration vulnerabilities and easier auditing of the entire stack.

### 3. ORM-First (Ent)
- **Principle:** All database interactions happen through a typed ORM.
- **Security Benefit:** Virtually eliminates SQL Injection risks by enforcing parameterized queries and typed schema definitions.

---

## 🛡️ Security Hardening

### 1. Authentication & Session Management
- **Passwords:** All passwords (user and share) are hashed using **Argon2id**, the winner of the Password Hashing Competition.
- **Sessions:** Secure cookies with `HttpOnly` and `SameSite=Lax`. Production mode strictly enforces the `Secure` flag when behind a reverse proxy.
- **Rate Limiting:** Built-in protection for `/login` and `/unlock` endpoints (0.2 req/s) to prevent brute-force attacks.

### 2. Share Protection
- **Dual-Token Logic:** Links use a random public token, but the server only stores its **hash**. Even a database leak doesn't reveal valid share links.
- **One-Time Passwords:** Share passwords are shown only once upon creation.
- **Session Isolation:** Guests can only delete files they uploaded within their current session, unless the admin explicitly permits otherwise.

### 3. Input Validation & Sanitization
- **Open Redirect Protection:** All `next` parameters in the login flow are sanitized to prevent malicious external redirects.
- **XSS Prevention:** Leverages Go's `html/template` package, which provides context-aware auto-escaping.
- **Path Traversal:** File paths are constructed using hashes and internal IDs, never directly from user-supplied filenames.

### 4. Information Leak Prevention
- **Success ID Validation:** The UI logic for showing "Upload Success" badges validates every ID against the currently active share. This prevents probing for the existence of files in other shares.
- **Minimal Error Messages:** Production mode suppresses detailed error traces to prevent information disclosure.

---

## 🧹 Automated Maintenance
- **Mark-and-Sweep GC:** A background worker automatically deletes expired shares, incomplete TUS uploads, and orphaned blobs, adhering to the principle of **Data Minimization**.

## 🚀 Development Process
Ferry is built using a **Multi-AI Orchestration** approach:
- **Google Gemini:** High-level logic, UI polish, and architectural guidance.
- **OpenAI Codex:** Implementation, code reviews, and deep-dive debugging.
- **Human Oversight:** Kai Krakow acts as the "Conductor," defining goals, validating every phase, and performing final quality assurance.

Every release candidate undergoes a full SAST sweep and dependency scan before publication.
