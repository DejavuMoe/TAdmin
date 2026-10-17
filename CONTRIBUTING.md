# Contributing

TAdmin is a Go application with an embedded Mini App and a VitePress documentation site. Tool versions are pinned in `mise.toml`, `go.mod` and `package.json`.

GitHub is the public mirror and CI/release host. Issues and proposed patches are welcome there; maintainers apply accepted changes to Forgejo and let the mirror synchronize. Do not merge changes directly into the GitHub mirror: they can be overwritten by synchronization. Maintainer Git operations use only `ssh://git@ssh.via.moe/dejavu/TAdmin.git`.

Before submitting, explain the observed problem, expected behavior and relevant test. Keep mock Telegram traffic local and use synthetic profiles. No real bot token or AI key is needed for the tests.

```sh
go mod download
go vet ./...
go test -race ./...
python3 -m unittest discover -s deploy/laya -p 'test_*.py'
go run ./cmd/docgen
pnpm install --frozen-lockfile
pnpm docs:build
```

Go tests include admission recovery, access control, API handling, feed parsing, Mini App authentication and frozen Jev hint ablation. Tests do not prove Telegram client compatibility or Oracle A1 performance; use the [trial acceptance checklist](docs/operations/acceptance.md) for those.

The project is MIT licensed. Keep dependency and bundled skill license notices intact. Report sensitive security issues as described in [SECURITY.md](SECURITY.md).
