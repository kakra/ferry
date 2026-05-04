# Share Direction Semantics

This document is the current semantic contract for share direction terminology in ferry.

## Current Contract

- Share `type` is interpreted from the guest's point of view.
- `upload` means the guest is expected to upload files into the share.
- `download` means the guest is expected to download files from the share.
- Managers and owners may still upload files to either share type when they are preparing or maintaining the share.
- The effective workflow therefore depends on both role and share type.

## Role Perspective

- **Administrators** can manage users and inspect server status.
- **Share managers** can see and edit all shares.
- **Other authenticated users** can see and manage only the shares they own.
- **Guests** can only access the exact share link they were given and must still satisfy the share password or unlock flow when required.

## Important Interpretation Rule

Do not read `upload` and `download` as exclusive protocol restrictions.

- A guest-facing `upload` share is typically used to receive files from a guest.
- A guest-facing `download` share is typically used to send files to a guest.
- In both cases, uploads and downloads may occur depending on who is accessing the share.

## Planned Rename

The current `upload` / `download` terminology is intentionally kept for v1 compatibility.
For the next major release, the share types should be renamed throughout the codebase, UI, and documentation to terminology that matches the workflow direction more clearly, such as `Receive Share` and `Send Share`.
