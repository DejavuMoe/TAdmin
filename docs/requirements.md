# TAdmin v0.1.0 requirements

This document records the product decisions confirmed on 2026-09-16, narrowed to admission verification only. It describes the target behavior, not a claim that the current implementation already provides it. Previous implementation and verification records remain in `docs/progress.md`.

## Product and scale

- Private, self-hosted Telegram admission-verification bot for supergroups without forum topics, including channel-linked discussion groups. Basic `chat.type=group` compatibility is not a target; onboarding must check the actual Telegram chat type.
- One Go process and one Linux executable; publish CGO-free `linux/amd64` and `linux/arm64` builds.
- Target deployment: three groups with 500-1,000 members each. Join and attack rates remain unmeasured; earlier throughput estimates are not acceptance evidence.
- The first release aims to raise the cost of automated joins with a CAPTCHA. Passing the challenge is the admission criterion; detecting advertisements or other abuse after admission is deferred, and CAPTCHA effectiveness is not a guarantee that an entrant will not spam.
- Telegram updates use Long Polling. The embedded `net/http` verification service is exposed through the owner's existing domain and Nginx HTTPS reverse proxy; its backend must not be publicly reachable.
- SQLite uses `modernc.org/sqlite`; TOML provides deployment configuration and later Telegram commands provide per-group configuration.
- This project has not been deployed and has no production data. No legacy forum-management cutover or production data conversion is required for this release; keep existing migration files intact and use ordinary ordered schema migrations where needed.

## Join verification

The required admission flow is direct join followed by immediate restriction and verification. Existing Guard and ordinary application paths are optional compatibility paths, not additional first-release requirements or onboarding prerequisites:

1. Guard Bot Join Request Query plus Mini App.
2. Ordinary `ChatJoinRequest` plus immediate private verification.
3. Directly joined members temporarily restricted until verification.

- Restrict an untrusted direct entrant before offering verification. A failed restriction is an operational failure, not a successful protected admission.
- Channel-linked discussion groups must require users to join before commenting for the join-verification policy to cover commenters. Onboarding documents and live acceptance must verify this Telegram setting; do not assume every commenter generates a membership event. Linked-channel comment threads are not forum topics and must remain supported.
- Guard queries require an initial Mini App open or answer within Telegram's 10-second deadline, independently of the challenge lifetime.
- Private delivery may be unavailable for direct entrants; provide a user-bound private-chat deep link in the source group. Ordinary join-request private delivery must respect Telegram's short contact window.

Verification uses only randomized interaction challenges. The MVP does not include group knowledge questions, text answers, or a question bank. The final challenge design is a server-rendered 3x3 PNG grid with simple shapes and digits; the user clicks two requested targets in order. Color is decorative and is never the only distinguishing feature.

- Default total lifetime: 180 seconds, configurable per group.
- Maximum attempts: 3; a failed attempt invalidates its image and generates a new one without extending the original deadline.
- Sessions bind an opaque, at-least-128-bit token to the Telegram group, user, and join event/query.
- Correct cells, order, and semantics never appear in HTML attributes, JavaScript variables, or API responses.
- Verification state transitions are atomic and one-time; cross-user, cross-group, replayed, expired, and already-consumed submissions fail.
- Telegram Mini App `initData` is verified server-side, including HMAC and `auth_date`; `initDataUnsafe` is not trusted.
- Expiry automatically removes a directly joined member without a permanent ban, or declines a pending application. Do not add a 30-minute manual-review grace period after verification expires.
- Three failed attempts or an inaccessible verification path may enter manual review, but must not extend the original admission deadline. Direct entrants remain restricted until an authorized approval or removal.
- Successful users have TAdmin's admission restriction lifted and follow the group's current default permissions. Preserve any individual restriction already present in the join event; CAPTCHA success must not erase an administrator's earlier restriction. There is no new post-verification permission probation.
- Direct admission verification targets ordinary user accounts; the configured owner, current administrators, and Telegram bot accounts are exempt. Authorized manual approval is an explicit audited exception. Ordinary new entrants must solve the CAPTCHA. Previously verified re-entry trust, if retained, must be an explicit owner-controlled exception rather than a silent default bypass.

## Deferred post-admission moderation

- Do not inspect ordinary or edited group messages for advertisements, image content, repeated content, promotion links, or spam scores in the first-release runtime.
- Defer the punishment ladder, recent-member risk scoring, automatic Raid policy, OCR, external visual APIs, and automatic channel-identity enforcement. The earlier decision to block non-exempt external channel identities is deferred with this scope, not an active admission-verification requirement.
- Existing moderation code is not evidence that these features belong in the narrowed release. Implementation must remove or disconnect its message handlers, background action execution, and moderation commands from the verification-only runtime; changing this document alone does not disable the current implementation.
- Keep only admission-related restrictions, permission restoration, timeout removal, administrator recovery, and their audit records in scope. Do not add image-analysis dependencies or request further image-ad policy decisions.

## Manual review, permissions, and logs

- Admission review cards and verification logs default to the owner private chat. No extra group is required. An existing non-forum group, including the source group itself, can optionally serve as the shared destination. Every card and log identifies its source group by name and ID.
- The owner's primary management interface is the bot's private chat, with an explicit source-group selection for group-specific operations. Keep source-group commands available where useful; no Web administration dashboard is required.
- Review cards include source group, user identity, admission path, timestamps, attempts, and status. Manual admission decisions are explicit, attributable exceptions to CAPTCHA verification, never automatic passes on failure.
- Review actions are idempotent and bind to the saved management chat and a nonzero message ID. Admission-review deadlines never extend the original verification deadline.
- Only the global owner can onboard groups, change configuration, or bind the management group.
- The owner and current administrators of the source group can perform admission review and recovery. Telegram administrator status is checked at action time and is not permanently cached.
- Membership or administrator status in the optional review group alone grants no authority over a source group. Private-chat group selection also grants no authority by itself.
- Anonymous administrator commands are rejected because the actor cannot be audited reliably.
- Standard output uses structured JSON logs. Important events are copied to the management group's main chat and retained in SQLite according to policy. Respect Telegram flood limits and retry delays; aggregate repetitive notices so an attack does not turn management logging into a second flood.
- Never log a Bot Token, complete Mini App `initData`, challenge answer, signature, full invite link, cookie, or browser/IP fingerprint.
- User-facing content supports Simplified Chinese and English.

## Deployment configuration

- `owner_user_id`: required positive Telegram user ID.
- `database_path`: required SQLite file path.
- `listen_address`: default `127.0.0.1:8080` for native deployment. The Linux Compose template uses host networking with the same loopback listener; no public backend port is published.
- `public_base_url`: absolute HTTPS URL, required for live onboarding; may be empty for offline development.
- `management_chat_id`: optional; omitted or zero uses the owner private chat. A configured group may be the source group itself. This global destination is shared by groups onboarded with that configuration.
- `default_language`: `zh-CN` or `en`.
- Bot Token: required at live startup and read only from `TADMIN_BOT_TOKEN`.
- Support both Docker Compose and a single binary with systemd. Both run the same application and SQLite schema behind the existing Nginx; Compose persists the database directory, including WAL sidecar files, and runs one bot instance.

## Storage and retention

- SQLite enables WAL, foreign keys, and a busy timeout; migrations and web assets are embedded in the binary.
- Required state covers managed groups, verification sessions, recoverable join actions, admission reviews, and audit events. Existing migration tables need not be rebuilt solely to remove unused moderation columns or tables.
- Default retention: completed verification sessions up to 24 hours; completed admission review and audit events 30 days; group configuration until explicitly changed. Preserve active verification and recoverable actions during cleanup.
- One process only; no leader election, distributed locking, external database, Redis, queue, or multi-instance guarantees.

## Explicitly out of scope

- Web administration dashboard, separate administrator accounts, Prometheus, plugins, microservices, event buses, ORMs, dependency-injection frameworks, complex rule scripting, SaaS multi-tenancy, telemetry, automatic online updates, a required Docker deployment, Kubernetes, and a local Telegram Bot API server.
- Node.js and frontend build chains, React/Vue/Vite/Tailwind, external fonts/assets, marketing pages, analytics, gradients, glass effects, and dashboard layouts.
- Claims that TAdmin detects AI, Telegram account age, phone numbers, device identity, IP address, or Telegram-wide abuse history.

## Implementation and acceptance priorities

Historical stage completion does not establish acceptance of this narrowed scope. Reuse the existing verifier and join workflow, then validate the actual direct-join flow in disposable Telegram groups.

1. Align the runtime with verification-only scope, management without forum topics, and owner private-chat controls.
2. Verify immediate restriction, private deep-link delivery, CAPTCHA identity binding, success restoration, expiry removal, administrator authorization, and restart recovery.
3. Supply Docker Compose and binary/systemd deployment behind the existing Nginx with persistent SQLite storage.
4. Exercise real joins and channel-comment entry in disposable test groups, including failure and restart cases; confirm ordinary post-admission messages are not scored or automatically punished.

Official Telegram contract reviewed on 2026-09-17 (Asia/Singapore): Bot API 10.3. The module requires Go 1.27.0; offline candidate builds use the verified Go 1.27.1 toolchain.

- https://core.telegram.org/bots/api
- https://core.telegram.org/bots/api-changelog
- https://core.telegram.org/api/discussion
- https://go.dev/doc/devel/release
