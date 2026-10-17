# Project instructions

- Keep the single Go process + SQLite architecture. Fix shared causes with focused regressions; avoid adding services or abstractions for optional features.
- Admission verification, CAS and Jev are separate choices. Keep Jev advisory for the current trial, retain its current questions and thresholds, and leave Laya experimental. Do not add group-message AI moderation.
- For AI integration changes, use `.agents/skills/typesafe-ai/SKILL.md` and consult live TypeSafe documentation. Documentation, deployment and unrelated fixes do not require AI research.
- Edit canonical source. On the maintainer's Windows workstation, use `linux-task.ps1 -Mode build -Project <Windows checkout> -Command '<command>'` for Linux checks; never edit only the disposable WSL mirror or mutate Git there.
- Run `go vet ./...`, `go test -race ./...`, `go run ./cmd/docgen -check`, the offline Python tests and `pnpm docs:build` for a release candidate. CI also checks dependencies, secrets, workflows and both Linux image architectures. See `docs/operations/releasing.md`.
- Regenerate documentation data with `go run ./cmd/docgen`; do not hand-edit generated files or relabel frozen evaluation fixtures to improve scores.
- Maintainer Git remote is only `ssh://git@ssh.via.moe/dejavu/TAdmin.git`. Forgejo mirrors to GitHub, where Actions runs. Do not add a GitHub push remote or merge directly on the mirror.
- Never commit credentials, user sessions or production databases. A passing local check is not a passing GitHub run; a tag or release is not authorization to deploy.
