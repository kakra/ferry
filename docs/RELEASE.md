# Release Policy

This document defines the release rules for ferry after the first public release.

## Versioning

- ferry follows semantic versioning after `v1.0.0`.
- Breaking changes are only allowed between major releases.
- Minor releases may add features, but must preserve documented behavior and supported configuration.
- Patch releases are limited to bug fixes, security fixes, and documentation corrections.
- A maintained changelog is required starting with the first public release.

## Feature Flags

- Feature gates and long-lived feature flags should be avoided.
- A feature should normally be released only when it is ready to become part of the supported product.
- Exceptions are allowed only when a flag is required for safety, migration, or operational compatibility.
- Any exception must be documented with its purpose, default, risk, and planned removal point.

## Deprecation Flags

- Deprecation flags are allowed to prepare the next major release.
- They may expose new breaking behavior early for testing before the major release.
- They must default to the current stable behavior until the next major release.
- The deprecated path and its flag must be removed during the next major release cleanup.
- Release notes must clearly state when a deprecated behavior becomes unsupported.

## Upgrade Compatibility

Starting with `v1.0.0`, release documentation must describe supported upgrade paths only.
Unsupported development snapshots are not part of the public release contract.

## Docker Hub Release Preparation

Publishing official images to Docker Hub requires both project metadata and an automated image pipeline.

Required repository decisions:
- Docker Hub namespace and image name, for example `kakra/ferry` or an organization-owned `ORG/ferry`.
- Supported architectures, at minimum `linux/amd64`; `linux/arm64` is recommended.
- Tag policy:
  - `vX.Y.Z` for immutable releases.
  - `vX.Y` and `vX` as moving convenience tags if desired.
  - `latest` only for the latest stable release, not unstable builds.
  - `rc` or `vX.Y.Z-rc.N` only if pre-release images should be public.

Required image hygiene:
- The image must run with generated secrets or explicit `dev_mode`; release images must not start successfully with default secrets.
- Runtime data must stay outside the image in mounted volumes for `config.yaml`, database, and storage.
- The Dockerfile should keep using BuildKit cache mounts for fast local and CI builds.
- The final image should include only the binary, templates, i18n files, CA certificates, timezone data, and required runtime directories.
- The image should include OCI labels for source, license, version, revision, and description.

Required automation:
- A CI job must build and test before publishing.
- A release job must authenticate to Docker Hub with repository secrets.
- A release job should build multi-architecture images with Docker Buildx.
- Images should be pushed only from protected release tags or an explicit release workflow.
- The release workflow should publish the exact Git revision used for the source release.

Recommended verification before pushing:
- `go test ./...`
- `go vet ./...`
- `docker build .`
- `docker run` or `docker compose` smoke test against `/health`
- Bootstrap smoke test using `ferry init-config` with mounted config and data volumes

Docker Hub account prerequisites:
- Docker Hub account or organization.
- Repository created or permission to create it.
- Access token with push permissions stored as CI secrets.
- Optional: repository description, README sync, logo, categories, and supported platform metadata.

## GitHub Pages Documentation

The documentation site is deployed from `docs/` through GitHub Actions. The workflow copies `CHANGELOG.md` and `SECURITY.md` into the Pages source before running the Jekyll build.

Repository prerequisites:
- Enable GitHub Pages with **GitHub Actions** as the source.
- Keep release-facing documentation in `docs/`.
- Keep critical disclosure instructions in `SECURITY.md`.
- Verify the `Deploy Documentation` workflow after the first public push.
