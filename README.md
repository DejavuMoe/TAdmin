# TAdmin

TAdmin is a private, self-hosted Telegram supergroup verification and basic moderation bot. The v0.1.0 plan targets one statically built Go process managing 1-5 supergroups, with SQLite persistence, Long Polling, and an embedded HTTP service behind an existing HTTPS reverse proxy.

Stages 1-5 provide the runnable foundation, verification engine, all three Telegram join paths, management-topic manual review, deterministic anti-spam scoring, the punishment ladder, Raid protection, owner-managed invitation links, retention cleanup, and production deployment guidance. Live Telegram acceptance and the final v0.1.0 tag remain deferred to stage 6.

## Current capabilities

- Go 1.26.5 application with JSON `slog` output and graceful shutdown.
- TOML deployment configuration; the Bot Token is read only from `TADMIN_BOT_TOKEN`.
- SQLite initialization with embedded, repeatable migrations, WAL, foreign keys, and a busy timeout.
- Server-rendered 3x3 PNG challenges with two ordered shape-and-digit targets, a fixed 180-second deadline, three attempts, one-time completion, and atomic SQLite transitions.
- Telegram Mini App `initData` HMAC-SHA-256 validation with a five-minute authorization window, future-time rejection, and session/user/source binding.
- Embedded native HTML, CSS, and JavaScript Mini App with Simplified Chinese and English text, Telegram theme variables, touch interaction, and no frontend build chain.
- Same-origin verification page and API with strict JSON limits, per-session/client rate boundaries, no-store responses, and security headers.
- Telegram Long Polling with an explicit `allowed_updates` list.
- A narrow extended update decoder and raw Bot API adapter for Guard `query_id`, `sendChatJoinRequestWebApp`, and `answerChatJoinRequestQuery`; supported standard methods still use `github.com/go-telegram/bot`.
- Guard Mini App, ordinary join-request private verification, and direct-join restriction flows with source-key idempotence and restart recovery.
- Fixed-deadline expiry scanning, 30-day re-entry trust, current-default-permission restoration, and recent-member state initialization.
- One forum management topic per managed group with 30-minute manual review, approve-once, approve-and-trust, decline, and ban actions.
- Real-time owner/source-administrator authorization, transactional decision claiming, non-permanent direct-member kicks, and permanent bans only for the explicit ban action.
- Normal and edited message moderation for text and media captions, including correct Telegram UTF-16 entity slicing, `url` and `text_link` handling, normalized Unicode, full-width ASCII, invisible controls, sanitized domains, and external `sender_chat` identity.
- HMAC-SHA-256 content fingerprints derived from the Bot Token in memory; message text, captions, full URLs, forward details, and secrets are not persisted or copied to management topics.
- Explainable recent-link, Telegram-promotion, obfuscated-link, mention, message/media-rate, self-repeat, coordinated-repeat, and recent-forward/media rules with rolling 24-hour scores.
- Transactional 3/5/8/12 punishment escalation: delete and warn, 10-minute mute, 24-hour mute, then permanent ban. Raid-only weight cannot create a permanent-ban score.
- Recoverable SQLite moderation actions with unique keys, atomic claims, failure retry, current-administrator checks, concise group notices, audit events, and sanitized management-topic logs.
- Group allowlists and blocklists, owner/current-source-administrator daily punishment commands, and explicit blocklist rejection at the front of all join paths.
- Per-group automatic/forced-on/forced-off Raid state, 12 joins in 60 seconds and coordinated-repeat triggers, simple verification-failure/punishment counters, one-time transition notices, and a 15-minute quiet exit.
- Owner-only join-request invite creation, safe listing, and idempotent revocation. Full links are delivered only in the owner's private chat and stored only as Bot-Token-derived AES-256-GCM ciphertext with a keyed fingerprint.
- Non-replayable invitation creation and recoverable revocation state in SQLite, including private-delivery preflight, automatic revocation after delivery failure, limited retry, and explicit indeterminate results after an ambiguous create.
- Startup and daily batched retention cleanup for terminal verification, review, join, moderation, Raid, and audit data while preserving active or retryable work.
- Hardened systemd, Nginx, Caddy, Cloudflare Tunnel, backup, restore, upgrade, rollback, and troubleshooting guidance.
- Private `/start`, deep-link `/start verify_...`, owner-only `/tadmin status`, and idempotent owner-only `/tadmin setup` onboarding.
- `GET /healthz` with no configuration or secret disclosure.
- CGO-free Linux amd64 and arm64 release builds with SHA-256 checksums.

## Requirements

- Go 1.26.5 or a compatible Go 1.26.x toolchain.
- GNU Make plus `sha256sum` for release builds.
- A Telegram Bot Token for live startup. A real token and group are not needed for unit tests or cross-compilation.

## Configuration

Copy `config.example.toml` to an untracked `config.toml`, then replace the example owner ID:

```sh
cp config.example.toml config.toml
export TADMIN_BOT_TOKEN='replace-with-a-test-token'
```

The token must never be written into TOML, source files, Git, or command output. `public_base_url` may remain empty for tests and offline development, but `/tadmin setup` refuses to enable a group until it is an absolute HTTPS URL. Setup also requires `management_chat_id` to identify a forum-enabled management supergroup. The HTTP listener remains restricted to a loopback address such as `127.0.0.1:8080`.

Validate the binary version without a config file or token:

```sh
go run ./cmd/tadmin --version
```

Start the bot after configuring the environment:

```sh
go run ./cmd/tadmin --config config.toml
```

## Telegram commands

- Private `/start` returns a short description and version.
- Private `/start verify_{token}` validates the bound user and opens the existing Mini App session; it is used as the direct-join group-message fallback.
- `/tadmin status` is owner-only and reports uptime, database health, Join Request Query capability, and enabled managed-group count.
- `/tadmin setup` is owner-only and must be run in the source supergroup. It checks source and management-group permissions, creates or reuses one management topic, proves it can send and edit there, and only then enables join verification.
- `/tadmin allow add <user_id>` and `/tadmin allow remove <user_id>` are owner-only.
- `/tadmin block add <user_id>` and `/tadmin block remove <user_id>` are owner-only. The latest explicit add wins; if legacy/conflicting rows exist, block takes precedence until changed.
- `/tadmin raid auto|on|off` and `/tadmin moderation status` are owner-only and operate on the current managed supergroup.
- `/tadmin invite create <name>`, `/tadmin invite list`, and `/tadmin invite revoke <public_id>` are owner-only and operate on the current enabled managed supergroup. Creation always requests administrator approval; a complete invite URL is never posted to the group.
- `/tadmin warn`, `/tadmin mute 10m`, `/tadmin mute 24h`, `/tadmin kick`, and `/tadmin ban` accept a replied-to user or an explicit user ID. The owner and current source-group administrators may use them; anonymous administrators are rejected.

## Verification HTTP surface

Sessions are created only by the internal Go join service; there is no public test-session or bypass route. The embedded HTTP service exposes:

```text
GET  /verify/{session_token}
POST /api/verify/{session_token}/open
GET  /api/verify/{session_token}/image/{generation}.png
POST /api/verify/{session_token}/answer
```

The two POST routes accept only `application/json`. Telegram `initData` is carried in the request body, never in a URL. The browser receives the rendered PNG and a human-readable prompt but never receives cell semantics, target indices, or the correct order as structured data.

## Join and review behavior

- A Guard query opens the HTTPS Mini App immediately. A successful challenge answers `approve`; initialization failure, challenge failure, or expiry answers `queue` and creates one review item.
- An ordinary join request immediately uses its short-lived `user_chat_id` private channel. It is approved only after success; delivery failure, challenge failure, or expiry goes to review.
- A directly joined non-admin member is restricted before a session is delivered. Private Web App delivery is preferred, an ephemeral group hint is optional, and a normal group URL deep link is the reliable fallback. Success restores the current group permissions returned by `getChat`.
- Review authorization is checked against the global owner or the source group's current Telegram administrators for every callback. A pending review times out after 30 minutes: applications are declined and directly joined members are banned then unbanned so they may apply again.

The join coordinator persists source keys, current Telegram actions, review deadlines, and card IDs. It scans SQLite for fixed-deadline expirations and pending/processing work after restart; no Redis, queue, or event bus is used.

## Moderation and Raid behavior

Recent membership means both `now < recent_until` and fewer than three normal messages. A low-risk, unpunished normal message advances that counter; the fixed 24-hour deadline is never extended. Users without a member-state row are treated as established members, so an established member's ordinary external link receives no score by itself.

Default deterministic rules are:

- recent external link: +1;
- recent Telegram promotion: +3; established batch promotion: +2;
- obfuscated or mismatched link: +2;
- 5 distinct mentions: +2; 10 distinct mentions: +3;
- 6 messages in 10 seconds: +3;
- 3 recent-member media/forward messages in 30 seconds: +2;
- same-user repeat in 60 seconds: +3;
- the same digest from 3 recent members in 5 minutes: +5 and a strong Raid signal;
- an immediate recent-member forward or promotional media message: +1.

Automatic Raid also enters at 12 join cases in 60 seconds, 6 verification failures in 5 minutes, or punishments of 4 distinct recent members in 5 minutes. While active, it adds one point to each already-matched recent-member rule; it never creates risk for a normal message, changes challenge attempts/deadlines/review behavior, manages invitations, or supplies points needed for a permanent ban. `forced_on` never exits automatically, `forced_off` still records signals, and `auto` exits after 15 quiet minutes.

## Invitation security and retention

Invitation creation first proves that the owner can receive a private message. TAdmin then creates an additional Telegram link with `creates_join_request=true`, encrypts it before persistence, sends the complete link only to the owner, and posts only its safe local identifier to the source group. A repeated Telegram update cannot create or deliver a second link. If final private delivery fails, revocation begins immediately.

The encryption and fingerprint keys are separately derived from `TADMIN_BOT_TOKEN`. Rotating that Token means existing active links can no longer be decrypted for automated revocation; follow the rotation procedure in [docs/operations.md](docs/operations.md).

Retention runs at startup and every 24 hours using transactions of at most 500 deletions. Terminal verification sessions are retained for 24 hours; completed operational history is retained for 30 days. Pending, processing, failed, active, and otherwise retryable state is preserved. TAdmin never runs automatic `VACUUM`.

## Production deployment

The shortest production path is:

1. install the matching binary as `/usr/local/bin/tadmin`;
2. keep TOML in `/etc/tadmin/config.toml` and the Bot Token only in `/etc/tadmin/tadmin.env`;
3. store SQLite under `/var/lib/tadmin`;
4. install [deploy/systemd/tadmin.service](deploy/systemd/tadmin.service);
5. expose only `/verify/` and `/api/verify/` through the supplied [Nginx](deploy/nginx/tadmin.conf) or [Caddy](deploy/caddy/Caddyfile) example;
6. confirm `http://127.0.0.1:8080/healthz`, then run `/tadmin setup` in each source group.

See [docs/deployment.md](docs/deployment.md) for installation and HTTPS setup, and [docs/operations.md](docs/operations.md) for retention, backup, restore, upgrade, rollback, Token rotation, logs, and troubleshooting.

## Verification and release

```sh
go test ./...
go vet ./...
make release
```

Release files are written to `dist/`:

```text
tadmin-linux-amd64
tadmin-linux-arm64
SHA256SUMS
```

These artifacts are stage 5 release candidates, not the final v0.1.0 release. The complete frozen requirements and phase boundaries are recorded in `docs/requirements.md`; current implementation and the stage 6 live acceptance checklist are in `docs/progress.md`.
