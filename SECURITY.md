# Security policy

Security fixes target the current `master` and the latest published release. There is no long-term support commitment for older releases.

Please use GitHub's **Security → Report a vulnerability** on `DejavuMoe/TAdmin` if private reporting is enabled. If unavailable, open an issue asking for a private reporting channel without publishing exploit details, credentials, real profiles or database contents.

Include the affected commit/version, deployment mode, reproduction using synthetic data, expected behavior and observed impact. Local source review, automated scanning and mock API tests have different limits; identify which evidence you have.

Treat the bot token, TypeSafe key, userbot session and database backups as secrets. A userbot session grants account access. Keep the HTTP listener on loopback, place the optional Mini App behind HTTPS, and retain the default feed SSRF protection. See [deployment](docs/guide/deployment.md) and [backup](docs/operations/backup.md).

CI uses CodeQL, `govulncheck`, dependency review, `pnpm audit` and Gitleaks. These checks do not establish the absence of vulnerabilities. Jev scores are advisory signals, not a security boundary or a verified judgement about a person.
