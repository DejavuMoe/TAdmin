# TAdmin implementation progress

Last updated: 2026-08-09

## Stage status

| Stage | Status | Scope |
|---|---|---|
| 1 | Complete | Runnable foundation |
| 2 | Complete | Random interaction verification and Mini App |
| 3 | Complete | Three join paths and manual review |
| 4 | Complete | Anti-spam, punishments, and Raid |
| 5 | Complete | Invitations, retention, deployment and candidate release packaging |
| 6 | Not started | Live Telegram acceptance and v0.1.0 release |

## Stage 1 implementation

- Go module pinned to Go 1.26.5 and explicit dependency versions.
- TOML configuration with strict unknown-field rejection and environment-only Bot Token.
- JSON structured logging, signal cancellation, bounded HTTP timeouts, request-body limit, and graceful shutdown.
- Embedded SQLite migration ledger and initial eight-table schema; WAL, foreign keys, and busy timeout enabled.
- Telegram Long Polling adapter with the six frozen `allowed_updates` values.
- A minimal raw `getMe` call retains Bot API 10.1's `supports_join_request_queries` field, which the pinned library model does not currently expose. Regular polling and standard methods continue to use `github.com/go-telegram/bot`.
- Private `/start`, owner-only `/tadmin status`, and owner-only supergroup `/tadmin setup` permission diagnostics.
- Simple `GET /healthz` endpoint.
- Necessary configuration, migration, owner authorization, and health tests.
- GNU Make targets for format, test, vet, local build, and CGO-free amd64/arm64 releases.

## Stage 1 boundary (historical)

No challenge image, Mini App, join state machine, review workflow, anti-spam rule, punishment action, Raid state, invitation management, or retention job has been implemented. `/tadmin setup` deliberately does not register or enable a group.

## Stage 1 verification

- `gofmt -w cmd internal assets`: passed.
- `go mod tidy`: passed; `go.mod` and `go.sum` are current.
- `go mod verify`: passed; all modules verified.
- `go test ./...`: passed.
- `go vet ./...`: passed.
- `make release`: passed with `CGO_ENABLED=0` for Linux amd64 and arm64.
- `sha256sum -c dist/SHA256SUMS`: both artifacts passed.
- `go version -m` confirmed Go 1.26.5, `GOOS=linux`, the requested architectures, and `CGO_ENABLED=0` for both artifacts.

Live Telegram behavior remains intentionally untested until stage 6, when a test Bot Token and test groups are available.

## Stage 2 implementation

- Added ordered `0002_verification_challenges.sql` migration without changing `0001`, extending verification sessions with source binding, language, generation, server-only challenge material, and one-time consumption time.
- Added a compact SQLite-backed state machine for `challenge_issued`, `challenge_passed`, `awaiting_review`, `expired`, and `cancelled`, while explicitly rejecting invalid `pending` transitions.
- All answer transitions run in SQLite transactions. A valid wrong answer increments attempts and rotates the generation; the third failure enters `awaiting_review`; success clears challenge material and can be consumed only once.
- Added 256-bit `crypto/rand` session tokens and server challenge secrets. The fixed default deadline is 180 seconds and is never extended by a retry.
- Added deterministic server-side 3x3 challenge derivation and standard-library PNG rendering. Each cell has a circle, triangle, or square, a programmatic seven-segment digit, and decorative color; two distinct targets are identified only by shape and digit.
- Added strict normalized-coordinate validation and server-only ordered answer checks, including rejection of invalid counts, non-finite values, and out-of-range positions.
- Added Telegram Mini App `initData` validation using the official bot-token HMAC-SHA-256 construction, constant-time hash comparison, a five-minute maximum age, 30-second future skew, required user identity, and optional source/start-parameter consistency checks.
- Added embedded native HTML, CSS, and JavaScript with Telegram theme variables, light/dark support, mobile touch targets, safe-area handling, Simplified Chinese and English text, and no frontend toolchain or telemetry.
- Added the verification page, open/image/answer APIs, strict JSON and 32 KiB endpoint body limits, no-store behavior, CSP and other security headers, explicit method/status handling, and bounded in-memory per-session and per-client request rates.
- Added an HTTPS-only verification URL builder that returns an explicit error when `public_base_url` is empty. No public session-creation or success-bypass endpoint exists.
- Added focused tests for challenge invariants and PNG decoding, ordered and invalid coordinates, HMAC/auth time/user/source binding, generation rotation, fixed expiry, three failures, terminal/replayed/concurrent answers, migration idempotence, HTTP boundaries, security headers, rate limiting, and response secrecy.

## Stage 2 boundary

The verifier is available only as an internal service plus its authenticated Mini App endpoints. It does not consume `ChatJoinRequest`, answer Guard queries, approve or decline applications, restrict or remove members, create review cards/topics, run moderation rules, punish users, or enter Raid mode. A third failed challenge records only `awaiting_review` for stage 3 to consume.

## Stage 2 verification

- `gofmt -w cmd internal assets`: passed.
- `go mod tidy`: passed; no new third-party dependency was added.
- `go mod verify`: passed; all modules verified.
- `go test ./...`: passed, including all stage 1 tests.
- `go vet ./...`: passed.
- `make release`: passed with `CGO_ENABLED=0` for Linux amd64 and arm64.
- `sha256sum -c dist/SHA256SUMS`: both artifacts passed.
- `go version -m` confirmed Go 1.26.5, `GOOS=linux`, the requested architectures, and `CGO_ENABLED=0` for both artifacts.
- No real Telegram token, join flow, browser matrix, Playwright run, stress test, fuzz campaign, or coverage target was used, as required by the stage boundary.

## Stage 3 implementation

- Added ordered `0003_join_workflows.sql` without changing earlier migrations. It adds per-group verification/trust durations, one minimal `join_cases` table with a unique hashed source key and recoverable Telegram action state, and the review fields needed for deadlines, card IDs, decisions, actors, and card recovery.
- Added a small SQLite join/review store. Action claims, review decisions, review timeouts, verification terminal transitions, trust writes, and recent-member initialization use guarded SQL updates or transactions so concurrent handlers have one winner.
- Added a lightweight join coordinator with an immediate wake channel plus periodic SQLite recovery. It proactively expires fixed-deadline sessions and resumes initializing, terminal, pending, failed, and stale-processing work after restart without a message queue or event bus.
- Added Guard query handling through a narrow raw adapter for only the three capabilities missing from `github.com/go-telegram/bot` v1.23.0. A narrow `getUpdates` decoder retains `ChatJoinRequest.query_id`; ordinary updates still pass through the existing bot handler.
- Added ordinary `ChatJoinRequest` delivery to `user_chat_id`, success-only approval, and review fallback when the short private-contact window is unavailable.
- Added direct-join detection from `chat_member`, real-time administrator/trust/application checks, immediate restriction, private and ephemeral delivery attempts, normal group deep-link fallback, current-default-permission restoration, leave cancellation, non-permanent kick, and explicit permanent ban behavior.
- Extended private `/start verify_...` with active-session and user binding checks. The verification API remains unchanged; terminal HTTP answers only wake the coordinator.
- Extended owner-only `/tadmin setup` into idempotent onboarding. It requires source rights, an HTTPS public URL, a configured forum management group, management-topic rights, and a successful topic send/edit probe before enabling the group.
- Added one management-topic review card per case, short opaque callback data, owner/current-source-admin authorization, 30-minute timeouts, approve-once versus approve-and-trust semantics, final card editing, and sanitized audit records.
- Added focused fake-Telegram and `httptest` coverage for extended update decoding, raw Guard calls, all three entry paths, delivery fallback, current permission restoration, source/update idempotence, trust, active expiry recovery, review timeouts and authorization, concurrent callbacks, kick versus ban, setup topic reuse, migration upgrade, and secret exclusion.

## Stage 3 boundary

No message-content rules, risk scores, punishment thresholds, timed mute ladder, Raid detector/commands, invitation creation or rotation, retention cleanup, deployment automation, or live Telegram acceptance was added. Ephemeral messages are used only as a best-effort enhancement and never as the sole direct-join verification path.

## Stage 3 verification

- `gofmt -w cmd internal assets`: passed.
- `go mod tidy`: passed.
- `go mod verify`: passed.
- `go test ./...`: passed, including all stage 1 and stage 2 tests.
- `go vet ./...`: passed.
- `make release`: passed with `CGO_ENABLED=0` for Linux amd64 and arm64.
- `sha256sum -c dist/SHA256SUMS`: both artifacts passed.
- `go version -m` confirmed Go 1.26.5, `GOOS=linux`, the requested architectures, and `CGO_ENABLED=0` for both artifacts.
- No real Telegram token/group, pressure test, browser matrix, Playwright run, fuzz campaign, or coverage target was used, as required by the stage boundary.

## Stage 4 implementation

- Added ordered `0004_moderation_and_raid.sql` without changing migrations 0001-0003. It extends managed groups and violations, and adds only current-message observations, per-target threshold cursors, recoverable moderation actions, and per-group Raid state/signals.
- Added in-memory Unicode NFKC/case folding, whitespace collapsing, full-width ASCII handling, zero-width/directional-control detection, UTF-16 entity slicing, plain/entity/`text_link` URL extraction, display-target mismatch detection, and lowercased host-only persistence.
- Added Bot-Token-derived HMAC-SHA-256 message/media fingerprints. SQLite, audit metadata, structured logs, and management topics retain only irreversible digests, rule IDs, scores, sanitized domains, Telegram identifiers, actions, and timestamps—not message/caption text or complete URLs.
- Added one transactionally idempotent current-version assessment path for `message` and `edited_message`, covering text and captions. It replaces an edited message's prior rules, rejects replayed/older updates, and attributes external channel posts to `sender_chat` instead of `GroupAnonymousBot` or `message.From`.
- Added the frozen balanced rules: recent external and Telegram links, obfuscated links, distinct-mention floods, 6 messages/10 seconds, recent media rate, same-sender repeat, coordinated recent-member repeat, and immediate recent forward/promotional media behavior.
- Reused `member_states` for the fixed 24-hour/first-three-normal-message window. Only a low-risk, unpunished normal message advances the counter; established members' ordinary external links receive no score.
- Added rolling 24-hour scores and transactional 3/5/8/12 threshold claiming. Actions are delete+warn, delete+10-minute mute, delete+24-hour mute, and delete+permanent ban, with exact Telegram `until_date` values and no repeated lower-threshold action.
- Added current-administrator and owner protection before every automatic Telegram action, allowlist bypass, blocklist explicit ban, external `sender_chat` delete/warn-only handling, manual kick as ban+unban, and permanent ban only for score 12, blocklist, or an authorized manual ban.
- Added owner-only allow/block list commands, Raid mode/status commands, and owner/current-source-administrator warn/mute/kick/ban commands with reply or explicit user ID targeting. Anonymous administrator identities remain rejected; allowlisting never creates verification trust.
- Added SQLite `pending/processing/completed/failed` moderation actions, atomic concurrent claims, replayable enforcement recovery, sanitized action results, audit events, group notices, and per-source management-topic logs. Group notices and management logs are separate actions; a stale in-flight send is closed as `indeterminate_after_restart` instead of being duplicated after restart.
- Added automatic, `forced_on`, and `forced_off` per-group Raid modes. Deterministic triggers are 12 joins/60 seconds, one coordinated-repeat signal/5 minutes, 6 verification failures/5 minutes, or 4 punished recent members/5 minutes; automatic mode exits after 15 quiet minutes.
- Raid adds one point only to already-matched recent-member rules. Base scores are stored separately so Raid-only weight cannot cross the permanent-ban boundary; challenge attempts, expiry, reviews, trust, and invitations remain unchanged.
- Added explicit blocklist handling at the front of Guard, ordinary-request, and direct-join initialization while preserving every non-blocklisted stage 3 path.

## Stage 4 boundary

No invitation-link creation/rotation/revocation, retention cleanup, systemd or reverse-proxy deployment guide, real Telegram acceptance, LLM/OCR/reputation service, browser automation, Web administration, rule DSL, plugin system, external database, queue, or event bus was added. The stage 2 verification HTTP contract and stage 3 review decisions are unchanged.

## Stage 4 verification

- `gofmt -w cmd internal assets`: passed.
- `go mod tidy`: passed; `golang.org/x/text v0.40.0` is the only new direct dependency.
- `go mod verify`: passed; all modules verified.
- `go test ./...`: passed, including all stage 1-3 tests and focused stage 4 tests.
- `go vet ./...`: passed.
- `make release`: passed with `CGO_ENABLED=0` for Linux amd64 and arm64.
- `sha256sum -c dist/SHA256SUMS`: both artifacts passed.
- `go version -m` confirmed Go 1.26.5, `GOOS=linux`, the requested architectures, and `CGO_ENABLED=0` for both artifacts.
- No real Telegram token/group, pressure test, browser matrix, Playwright run, fuzz campaign, or coverage target was used, as required by the stage boundary.

## Stage 5 implementation

- Added ordered `0005_invitations_and_retention.sql` without changing migrations 0001-0004. It adds one compact invitation table with opaque public IDs, encrypted secret material, independent HMAC fingerprints, operation keys, bounded retry state, and timestamps.
- Added owner-only `/tadmin invite create <name>`, `/tadmin invite list`, and `/tadmin invite revoke <public_id>` commands for enabled managed supergroups. Current administrators who are not the global owner and anonymous administrators cannot use them.
- Invitation names are trimmed, limited to 32 Unicode code points, and restricted to letters, numbers, spaces, and a small punctuation set. URLs, control characters, line breaks, and empty values are rejected.
- Invitation creation first proves that the bot can privately reach the owner, then creates only a Telegram `creates_join_request=true` link through the pinned library's standard API. The complete URL is sent only in the final owner DM; source-group replies, management-topic summaries, list output, audits, and logs contain only the public ID, safe name, state, result class, and timestamps.
- Complete invitation URLs are encrypted with AES-256-GCM. The encryption key and a separate full-URL fingerprint key use Bot-Token-derived HMAC-SHA-256 with distinct domain labels; the random nonce and 80-bit command-safe public ID use `crypto/rand`. Authentication failure, tampering, or a rotated Token fails closed.
- Creation and revocation state claims use SQLite transactions. Duplicate create operation keys cannot create or resend another link, repeated revocation is idempotent, and concurrent workers have one claimant.
- A Telegram API rejection is stored as a known failure, with invitation-rights errors reported explicitly. A non-idempotent creation interrupted before its Telegram result is known becomes `indeterminate` and is never replayed. A link persisted but not confirmed as privately delivered is automatically queued for revocation after restart. Final-DM failure triggers immediate revocation and bounded background recovery when Telegram revocation fails.
- Active and retryable invitation ciphertext remains available only for recovery. Successful revocation clears the ciphertext and nonce; terminal invitation metadata is eligible for later retention cleanup.
- Added a retention runner that cleans once at startup and every 24 hours, stops on process cancellation, and reports only aggregate failure context. Cleanup uses foreign-key-safe transactions in batches of at most 500 and never disables foreign keys or runs `VACUUM`.
- Terminal verification sessions are retained for at most 24 hours unless referenced by active join or review work. Resolved workflow, moderation, audit, observation, and Raid-signal history is retained for at most 30 days. Expired trust, no-longer-recent member state, and safe orphan moderation subjects are also removed.
- Pending reviews; pending, processing, failed, or retryable external actions; active or revocation-pending invitations; group configuration; lists; unexpired trust; current Raid state; and current recent-member state are preserved.
- Added copyable systemd, Nginx, and Caddy examples plus deployment and operations guides for a loopback origin, HTTPS proxying, Cloudflare Tunnel, permissions, health checks, logs, WAL-safe backup and restore, upgrades, forward-only rollback, Token rotation, and troubleshooting.
- Kept the candidate package as one CGO-free binary for Linux amd64 and arm64 with `VERSION` override, `--version`, and `SHA256SUMS`. No `v0.1.0` tag, remote push, or GitHub Release was created.

## Stage 5 boundary

No live Telegram request, production proxy or systemd installation, browser-matrix run, pressure test, automatic invitation rotation, Raid-triggered invitation change, container packaging, Web administration, external database, queue, telemetry, or final release was added. Stage 1-4 challenge, join, review, moderation, punishment, and Raid semantics remain unchanged. Live behavior and final `v0.1.0` publication are deferred to stage 6.

## Stage 5 verification

- Recursive `gofmt -w` over `cmd`, `internal`, and `assets`: passed.
- `go mod tidy`: passed; no new third-party dependency was added.
- `go mod verify`: passed; all modules verified.
- `go test ./...`: passed, including all stage 1-4 tests and focused invitation, encryption, recovery, retention, migration-upgrade, command, and deployment-reference tests.
- `go vet ./...`: passed.
- `git diff --check`: passed.
- `make release`: passed with `CGO_ENABLED=0` for Linux amd64 and arm64.
- `sha256sum -c SHA256SUMS` from `dist`: both artifacts passed.
- `go version -m` confirmed Go 1.26.5, `GOOS=linux`, the requested architectures, `CGO_ENABLED=0`, the candidate version, and a clean final VCS revision for both artifacts.
- No real Bot Token, live group, Telegram network request, system service, proxy deployment, browser matrix, Playwright run, pressure test, fuzz campaign, coverage target, tag, push, or GitHub Release was used.

## Stage 6 live acceptance checklist

Stage 6 requires a dedicated test Bot Token supplied through a protected environment variable, one forum management supergroup, one or more managed test supergroups, an owner account, non-owner administrator and member accounts, and clients able to exercise private chat, join requests, and direct joins. Never paste the Token, complete Mini App `initData`, invitation URLs, or message bodies into task text, shell history, logs, commits, screenshots, or release notes.

1. Record the clean stage 5 commit and candidate checksums, back up the test database, and perform read-only checks of the configuration, public HTTPS Mini App URL, bot identity, management forum, managed-group IDs, owner ID, bot rights, and required Telegram capabilities before mutating any group state.
2. Confirm the Mini App loads through the public HTTPS proxy in light and dark themes, validates the bound user, rejects replay/cross-user/expired data, rotates its generation after a wrong ordered answer, keeps the original 180-second deadline, passes once, and reaches review after three failures.
3. Exercise Guard query, ordinary join-request private delivery, and direct-join restriction paths separately. Verify success approval/restoration, failure and timeout behavior, private/ephemeral/group fallback delivery, current default-permission restoration, trust reuse, leave cancellation, and blocklist handling.
4. Exercise management-topic review creation, owner/current-source-administrator authorization, anonymous/non-admin rejection, approve once, approve and trust, reject, timeout, concurrent decisions, and final-card editing without exposing sensitive state.
5. Send only harmless synthetic anti-spam examples to verify text, caption, edited-message, UTF-16 entity, obfuscated-link, repeat, coordinated-repeat, rate, media, forward, `sender_chat`, allowlist, blocklist, owner, and current-administrator behavior. Confirm the 3/5/8/12 ladder, exact mute deadlines, manual actions, kick versus permanent ban, audit summary, and replay recovery.
6. Exercise automatic Raid entry and quiet exit plus `forced_on` and `forced_off`. Confirm its one-point recent-member adjustment cannot produce a permanent ban by itself and that Raid never creates, rotates, edits, or revokes an invitation.
7. Create, list, and revoke a join-request invitation as the owner. Verify preflight and final private delivery, safe group output, absence of the complete URL from database/log/audit/topic content, non-owner and cross-group rejection, idempotent repeat behavior, and revocation recovery. Test Token-rotation effects only in a controlled disposable setup.
8. Restart during active verification, pending join/review, failed or stale moderation work, unconfirmed invitation delivery, and revocation pending. Confirm safe recovery without duplicate non-idempotent creation or duplicate punishment/notice sends. Run retention against disposable aged fixtures or a backup copy and confirm active/retryable state survives.
9. Fix only defects revealed by this checklist. Re-run formatting, module verification, all tests, vet, diff checks, both CGO-free builds, checksum verification, and binary metadata inspection after every accepted code change.
10. After all live acceptance evidence is recorded, build `VERSION=v0.1.0` artifacts and verify clean VCS metadata. Creating an annotated local tag, pushing commits/tags, or creating a GitHub Release requires a separate, explicit owner authorization immediately before that action.

## Next stage

Stage 6 performs the checklist above against disposable Telegram groups, fixes only acceptance defects, and—only after all local and live checks pass—packages `v0.1.0`. It must not create a local tag, push, or publish a remote Release without explicit user authorization.

## 2026-09-17 — verification-only delivery candidate

This entry supersedes the old product scope and live-acceptance assumptions above; it does not rewrite historical results. The project has no production deployment or production data.

- Delivered the narrowed admission-verification runtime for three non-forum supergroups, including linked discussions. Removed moderation/invitation coordinator startup, message scoring, and their commands from the production route; historical packages/migrations remain isolated.
- Replaced forum setup with management-chat send/edit checks and private owner commands for explicit group onboarding, listing, status, and verification timeout. Saved review destinations bind callbacks to a nonzero message ID and current source-group authority.
- Made expiry directly remove/decline without a 30-minute extension. Preserved fixed deadlines across initialization failure/restart and successful CAPTCHA state across delayed restoration. Protected existing individual restrictions and administrator/banned/left member states.
- Corrected Mini App Guard identity fields, restricted membership interpretation, false API results, completed-application proof consumption, startup backlog ordering, and incomplete coordinate validation discovered by fuzzing.
- Added persistent polling offsets, database instance locking and Bot ID binding, shared Telegram flood wait, separate card delivery, completion audit/outbox records, explicit permission serialization, bounded PNG rendering, and an offline `--check` command.
- Added a non-root scratch Docker image, Linux Compose template, revised systemd/Nginx examples, and complete current deployment/operations/verification documents. No production service, Git tag, push, or remote release was created.
- Offline validation: full unit/integration tests and repeated race runs; vet and module verification; parser/coordinate fuzzing; govulncheck v1.8.0 reported no known reachable vulnerabilities; amd64/arm64 static builds and SHA-256; native and network-disabled read-only container SQLite checks; Compose, Nginx 1.28.0 and systemd template checks.
- Browser QA used existing Playwright/Chromium against the real local Go verifier with a synthetic Telegram bridge: desktop/mobile, light/dark theme variables, 320px layout, keyboard input, retries, terminal failure/success, missing authorization, and client clock skew. This is not a Telegram-client acceptance result.

Candidate artifacts are `dist/tadmin-linux-amd64`, `dist/tadmin-linux-arm64`, `dist/SHA256SUMS`, local image `tadmin:v0.1.0-rc.1`, and offline evidence under `dist/qa/`. See `docs/verification.md` for reproducible commands and the remaining real Telegram checklist. Do not declare production acceptance until that checklist is executed with actual credentials and disposable groups.

## 2026-09-17 — remove mandatory review-group setup

Corrected an unnecessary product constraint: admission verification does not require a second group. Review cards/logs now default to the owner private chat; an existing shared group, including the source itself, remains optional. Destination snapshots and live source-admin authorization remain intact. Local deployment configuration now selects the private default without changing the owner's other values or Token. This change sends no live Telegram messages and does not enable any real group.

## 2026-09-17 — real owner Mini App acceptance

The owner completed an actual Telegram-client CAPTCHA over an authorized temporary HTTPS tunnel. SQLite confirms one successful submission, consumed session state, and cleared challenge secret. This was an owner-only UI/authentication session with no join case and no enabled group, so it does not establish live member restriction/approval/removal behavior. The temporary tunnel and proxy were closed, the previous public URL setting restored, and the local bot restarted in its unenrolled state. See `docs/live-testing.md` for the exact evidence and remaining checks.
