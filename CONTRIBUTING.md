# Contributing

Thanks for contributing to Harmoniq Maestro.

## Scope

Supported OSS launch modules:

- `album-queue`
- `spotdl-wapper`
- `dynamic-playlists`
- `models`

Other directories are experimental and may change without notice.

## Development Setup

1. Install prerequisites:
- Go 1.23+
- Docker + Docker Compose
- MongoDB (optional if using Docker Compose)

2. Clone the repo and enter it:

```bash
git clone https://github.com/supperdoggy/SmartHomeServer.git
cd SmartHomeServer/harmoniq-maestro
```

3. Run core checks:

```bash
make test-core
make build-core-linux
```

## Lint and Security

Optional local tools:

- `golangci-lint`
- `gitleaks`

Commands:

```bash
make lint-core
make scan-secrets
```

Recommended pre-commit check:

```bash
gitleaks detect --source . --no-git --redact
```

## Pull Requests

Before opening a PR:

- Keep changes scoped and focused.
- Add or update tests for behavior changes.
- Update docs and `.env.example` for new config.
- Ensure CI passes.

## Commit Guidance

Use clear commit messages. Example:

- `fix(models): align import path for genre mappings`
- `chore(ci): add root security workflow`
