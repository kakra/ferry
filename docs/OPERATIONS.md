# Ferry Operations Guide ⛴️⚙️

This document provides essential instructions for deploying, maintaining, and recovering a ferry instance in production.

---

## 🚀 Deployment

### Docker (Recommended)
Ferry is optimized for containerized environments. Use the provided `docker-compose.yaml` as a starting point.

**Initial Configuration:**
Generate a local `config.yaml` with strong random secrets before the first start:

```bash
cp config.example.yaml config.yaml
docker compose run --rm ferry ./ferry init-config
```

The command creates `config.yaml` when it is missing and replaces known placeholder secrets in an existing file. Production starts reject default secrets unless `dev_mode: true` or `FERRY_DEV_MODE=true` is set explicitly.

**Configuration via Environment Variables:**
Any value in the `config.yaml` can be overridden by an environment variable with the prefix `FERRY_`. Nested structures use underscores as separators.

**Common Overrides:**
- `FERRY_SERVER_PUBLIC_URL`: The external URL (e.g., `https://share.example.com`). Required for link generation.
- `FERRY_SECURITY_BEHIND_REVERSE_PROXY`: Set to `true` (default) or `false`.
- `FERRY_AUTH_BOOTSTRAP_PASSWORD`: A strong secret for initial setup and recovery.
- `FERRY_SERVER_TRUSTED_PROXIES`: Comma-separated list of trusted proxy IPs.
- `FERRY_AUTH_RATE_LIMIT_ENABLED`: Enable or disable the rate limiter (`true`/`false`).
- `FERRY_DATABASE_PATH`: Path to the SQLite database.
- `FERRY_DEV_MODE`: Allows development defaults when set to `true`. Do not use for production.

### Storage Persistence
Ensure the following paths are mounted as persistent volumes:
- `/app/data`: Contains the SQLite database (`ferry.db`).
- `/app/storage`: Contains the Content-Addressable Storage (CAS) files.

---

## 🌐 Reverse Proxy Setup
Ferry is designed to run behind a reverse proxy (Nginx, Caddy, HAProxy, or UTM). 

### Nginx Configuration (Example)
To support large uploads via the TUS protocol, you **must** disable request buffering and allow large bodies:

```nginx
server {
    listen 443 ssl;
    server_name share.example.com;

    # 1. Allow large files (0 = unlimited)
    client_max_body_size 0;

    # 2. Disable buffering for TUS resumable uploads
    proxy_request_buffering off;
    proxy_buffering off;

    location / {
        proxy_pass http://ferry:8080;
        
        # 3. Essential headers for auth and rate limiting
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Trusted Proxies
To ensure the **Rate Limiter** sees the correct client IP, you must add your proxy's internal IP to the `server.trusted_proxies` list in `config.yaml` or via the `FERRY_SERVER_TRUSTED_PROXIES` environment variable.

---

## 💾 Backup & Restore

### Backup Strategy
A consistent backup must include both the database and the physical storage.

1. **Database:** Since Ferry uses SQLite in **WAL mode**, simply copying `ferry.db` might result in an inconsistent state.
   - **Recommended:** Use the SQLite `.backup` command or a filesystem snapshot that includes `ferry.db`, `ferry.db-shm`, and `ferry.db-wal`.
2. **CAS Storage:** Back up the entire `/app/storage` directory. Files in the CAS are immutable once written.

#### Backup Optimization (Optional)
If you use modern backup software (like `restic`, `borg`, `tar` with `--exclude-caches`, or `Bacula`), you can exclude the large CAS storage directory to save space and time. Since the physical files are transient (shares expire) and can be re-uploaded if necessary, backing up only the database might be sufficient for some use cases.

To signal backup programs to skip the storage directory, place a standard `CACHEDIR.TAG` file in the root of the storage directory:

```bash
# Inside the container or storage volume root
printf "Signature: 8a477f597d28d172789f06886806bc55\n" > data/storage/CACHEDIR.TAG
```

Ferry's internal cleanup process will explicitly ignore this file.

### Restore Procedure
1. Stop the ferry service.
2. Restore the physical files to `/app/storage`.
3. Restore the database file(s) to `/app/data`.
4. Restart ferry. The background worker will automatically verify storage integrity during its next run.

---

## 🧯 Recovery & Troubleshooting

### Break-Glass Mode
If you lose administrative access or misconfigure the system, use the built-in recovery mode.

1. **Stop the container/service.**
2. **Run the break-glass command:**
   ```bash
   # Inside the container or via CLI
   ./ferry break-glass
   ```
3. **Access:** The service will listen on `127.0.0.1:8081` (loopback only) and bypass normal user authentication.
4. **Login:** Use the `bootstrap_password` configured in your `config.yaml` or env.
5. **Repair:** Use the dashboard to reset admin passwords, enable accounts, or fix settings.

### Manual Admin Reset
If you cannot use break-glass mode, you can manually trigger the bootstrap flow by stopping the service and temporarily moving/renaming the `ferry.db` file (Note: this resets the entire system state).

---

## 📊 Monitoring
Monitor the **System Status** page regularly. 
- **Deduplication Ratio:** High ratios indicate storage efficiency.
- **Disk Free:** Ensure the host has sufficient space. CAS does not currently enforce hard quotas.
- **Cleanup Worker:** Verify the "Last Run" timestamp to ensure background maintenance is active.

---
**Maintenance Note:** The current "One-Page" dashboard is a deliberate design choice for the initial release to gather UX feedback. A split into separate "Shares" and "Users" pages may occur in future versions.
