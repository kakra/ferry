# Contributing to ferry

Thanks for contributing to ferry. Keep changes small, testable, and aligned with the project's operating principles.

## Before You Start

- Read [README.md](README.md) for deployment and development expectations.
- Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) before changing storage, database, auth, or cleanup behavior.
- Check [docs/ROADMAP.md](docs/ROADMAP.md) to avoid implementing against outdated assumptions.

## Development Setup

1. Install Go 1.26 or newer.
2. Copy `config.example.yaml` to `config.yaml`.
3. Start the app with `go run ./cmd/ferry`.

For container-based local runs, `docker compose up -d --build` is also supported.

## Change Rules

- Keep commits small and atomic.
- No feature commit without tests for the changed package.
- Run `gofmt` on changed Go files.
- Do not introduce external services beyond the existing Go + SQLite design.
- Preserve mandatory CAS and SQLite-as-source-of-truth assumptions.
- Do not document a feature as available until it is actually implemented and usable.
- Be transparent about AI-assisted work. ferry is developed primarily with AI support, but human review is always recommended before merge or release.
- The responsible human operator must remain the commit author. If an AI agent materially contributed to a commit, add the AI agent as `Co-authored-by` in the commit message.

## Validation

Every contribution should pass:

```bash
go test ./...
go vet ./...
```

If you change templates, configuration defaults, or user-facing behavior, update the relevant documentation in the same change.

Before the first public release, continue expanding coverage for authentication, migrations, integration flows, security cases, and container-based end-to-end checks as tracked in `docs/RELEASE_CHECKLIST.md`.

## Pull Requests

- Explain the problem and the chosen approach.
- Call out behavioral changes, migrations, or operational impact.
- Mention any follow-up work or known limitations clearly.
- If a change is intentionally incomplete, mark it explicitly as planned or stubbed in code and docs.
- State clearly when AI assistance was used in a meaningful way, especially for architectural, security-relevant, or user-facing changes.

## Security Reports

Do not report critical security vulnerabilities through the public issue tracker. Send critical security reports to **kai@kaishome.de** and include enough detail to reproduce or assess the issue.

## Scope Guidance

Good contributions fit the current direction of the project:

- reliability improvements
- bug fixes
- tests
- documentation corrections
- UX polish that does not fight the existing architecture

Changes that alter core architecture, storage semantics, or the security model should be discussed before large implementation work starts.
