# TAdmin v0.1.0 frozen requirements

This document records the final product baseline agreed on 2026-08-09. Later implementation stages may refine internal details but must not expand these product boundaries without an explicit decision.

## Product and scale

- Private, self-hosted Telegram supergroup verification and basic moderation bot.
- One Go process and one Linux executable; publish CGO-free `linux/amd64` and `linux/arm64` builds.
- Manage 1-5 supergroups of roughly 1,000 members each.
- Expected normal growth is 50-100 joins per day; estimated attack peak is 20 applications per minute.
- Primary abuse: advertising links, cryptocurrency scams, pornography accounts, gambling, channel promotion, and flooding.
- Telegram updates use Long Polling. The embedded `net/http` service listens only on loopback and relies on an existing Nginx, Caddy, or Cloudflare Tunnel HTTPS endpoint.
- SQLite uses `modernc.org/sqlite`; TOML provides deployment configuration and later Telegram commands provide per-group configuration.

## Join verification

TAdmin must ultimately support three independent paths:

1. Guard Bot Join Request Query plus Mini App.
2. Ordinary `ChatJoinRequest` plus immediate private verification.
3. Directly joined members temporarily restricted until verification.

Verification uses only randomized interaction challenges. The MVP does not include group knowledge questions, text answers, or a question bank. The final challenge design is a server-rendered 3x3 PNG grid with simple shapes and digits; the user clicks two requested targets in order. Color is decorative and is never the only distinguishing feature.

- Default total lifetime: 180 seconds, configurable per group.
- Maximum attempts: 3; a failed attempt invalidates its image and generates a new one without extending the original deadline.
- Sessions bind an opaque, at-least-128-bit token to the Telegram group, user, and join event/query.
- Correct cells, order, and semantics never appear in HTML attributes, JavaScript variables, or API responses.
- Verification state transitions are atomic and one-time; cross-user, cross-group, replayed, expired, and already-consumed submissions fail.
- Telegram Mini App `initData` is verified server-side, including HMAC and `auth_date`; `initDataUnsafe` is not trusted.
- Three failed attempts or an inaccessible verification path goes to manual review.
- Successful users immediately regain the group's current default permissions. There is no post-verification permission probation.
- A recent-member risk window remains for 24 hours or the first 3 normal messages, whichever ends first. It changes moderation scoring only, never Telegram permissions.
- Verified re-entry trust is configurable per group and defaults to 30 days.

## Moderation and Raid

- Balanced deterministic rules plus risk scoring; no LLM, OCR, third-party reputation service, or user profiling.
- Inspect normal and edited messages, text and captions, Telegram entities, normalized Unicode, zero-width characters, disguised links, Telegram promotion links, forwarding, repeated content, mentions, and message/media rates.
- Recent-member link and promotion behavior receives higher risk without banning normal media or links by default.
- Default 24-hour score thresholds: 3 delete and warn, 5 mute for 10 minutes, 8 mute for 24 hours, and 12 ban.
- Direct bans are limited to an explicit blacklist, an administrator action, or a user who reappears through an abnormal path while permanently banned.
- Raid detection considers join volume, verification failures, duplicate content from recent members, and punished recent members. Default join trigger is 12 applications in 60 seconds; the system can enter automatically and be overridden manually.
- Raid raises recent-member risk weights but does not remove all three attempts, automatically revoke invitations, or automatically permanently ban users. It exits after 15 quiet minutes.
- Group-level allowlists and blocklists are supported. Message bodies are not persisted by default; normalized hashes, extracted domains, rule IDs, scores, Telegram IDs, actions, and timestamps may be stored.

## Manual review, permissions, and logs

- One global forum-enabled management supergroup; each managed group has one topic.
- Review cards include source group, user identity, invite source, path, timestamps, attempts, status, and rules, with approve once, approve and trust, decline, and ban actions.
- Review actions are idempotent. Guard/ordinary applications time out to decline after 30 minutes; already joined members time out to a non-permanent kick.
- Only the global owner can onboard groups, change configuration, bind logs, manage invite links, or tune Raid behavior.
- The owner and current administrators of the source group can later perform review and daily punishments. Telegram administrator status is checked at action time and is not permanently cached.
- Anonymous administrator commands are rejected because the actor cannot be audited reliably.
- Standard output uses structured JSON logs. Important events are later copied to the relevant management topic and retained in SQLite according to policy.
- Never log a Bot Token, complete Mini App `initData`, challenge answer, signature, full invite link, cookie, or browser/IP fingerprint.
- User-facing content supports Simplified Chinese and English.

## Deployment configuration

- `owner_user_id`: required positive Telegram user ID.
- `database_path`: required SQLite file path.
- `listen_address`: loopback only; default `127.0.0.1:8080`.
- `public_base_url`: absolute HTTPS URL; may be empty through stage 1 for offline development.
- `management_chat_id`: optional until the management workflow is implemented.
- `default_language`: `zh-CN` or `en`.
- Bot Token: required at live startup and read only from `TADMIN_BOT_TOKEN`.

## Storage and retention

- SQLite enables WAL, foreign keys, and a busy timeout; migrations and web assets are embedded in the binary.
- Core tables: `managed_chats`, `verification_sessions`, `trusted_users`, `member_states`, `list_entries`, `violations`, `review_items`, and `audit_events`.
- Default retention: completed verification sessions up to 24 hours; review, violation, and audit events 30 days; verification trust 30 days; lists and group configuration until explicitly changed.
- One process only; no leader election, distributed locking, external database, Redis, queue, or multi-instance guarantees.

## Explicitly out of scope

- Web administration dashboard, separate administrator accounts, Prometheus, plugins, microservices, event buses, ORMs, dependency-injection frameworks, complex rule scripting, SaaS multi-tenancy, telemetry, automatic online updates, a required Docker deployment, Kubernetes, and a local Telegram Bot API server.
- Node.js and frontend build chains, React/Vue/Vite/Tailwind, external fonts/assets, marketing pages, analytics, gradients, glass effects, and dashboard layouts.
- Claims that TAdmin detects AI, Telegram account age, phone numbers, device identity, IP address, or Telegram-wide abuse history.

## Six implementation stages

1. Runnable foundation: config, SQLite, Telegram adapter, permissions, health, tests, and cross-builds.
2. Random interaction challenge and Mini App, including HMAC, expiry, retries, and one-time state.
3. Three join paths and manual review.
4. Anti-spam scoring, punishments, edited messages, channel identity, and Raid.
5. Invitations, retention, systemd/reverse-proxy deployment, documentation, and final release packaging.
6. Live Telegram acceptance with a test Bot Token, test groups/accounts, fixes, and v0.1.0 release.

Official version baseline checked on 2026-08-09: Telegram Bot API 10.2 (Guard Join Request Queries were added in 10.1) and Go 1.26.5.

- https://core.telegram.org/bots/api
- https://core.telegram.org/bots/api-changelog
- https://go.dev/doc/devel/release
