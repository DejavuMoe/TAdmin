# TAdmin operations

## Health and logs

The local health check exercises the HTTP process and SQLite connection without returning configuration or secrets:

```sh
curl --fail --silent http://127.0.0.1:8080/healthz
```

Use systemd for process status and JSON logs:

```sh
sudo systemctl status tadmin
sudo journalctl -u tadmin --since today --no-pager
sudo journalctl -u tadmin -f
```

Do not paste a Bot Token, complete `initData`, verification URL, full invite link, or user message body into diagnostics. TAdmin logs Telegram identifiers and sanitized operation states where necessary.

## SQLite retention

TAdmin runs retention cleanup once at startup and every 24 hours. Each SQLite transaction deletes at most 500 rows, and cleanup never runs `VACUUM` automatically.

- terminal verification sessions: 24 hours;
- completed reviews, terminal join cases, violations, audits, message observations, completed moderation actions, Raid signals, and terminal invite records: 30 days;
- expired verification trust and member states that are no longer recent: removed when eligible;
- active work, retryable work, active/revoking invites, lists, group configuration, current Raid state, and recent-member state: preserved.

A cleanup error is logged without sensitive record content and retried during the next cycle.

## Backup

SQLite uses WAL mode. Do not copy only `tadmin.db` while TAdmin is running, because committed pages may still be in `tadmin.db-wal`.

The simplest consistent backup is a short service stop:

```sh
sudo systemctl stop tadmin
sudo install -d -o root -g root -m 0700 /var/backups/tadmin/tadmin-YYYYMMDD
sudo cp --preserve=mode,ownership,timestamps /var/lib/tadmin/tadmin.db /var/backups/tadmin/tadmin-YYYYMMDD/
if [ -f /var/lib/tadmin/tadmin.db-wal ]; then sudo cp --preserve=mode,ownership,timestamps /var/lib/tadmin/tadmin.db-wal /var/backups/tadmin/tadmin-YYYYMMDD/; fi
if [ -f /var/lib/tadmin/tadmin.db-shm ]; then sudo cp --preserve=mode,ownership,timestamps /var/lib/tadmin/tadmin.db-shm /var/backups/tadmin/tadmin-YYYYMMDD/; fi
sudo systemctl start tadmin
curl --fail --silent http://127.0.0.1:8080/healthz
```

Keep the database and any sidecar files found after shutdown together as one snapshot. Do not copy the three files at different times while TAdmin is running.

If the host provides the official `sqlite3` CLI, its online backup command can create a consistent snapshot without stopping TAdmin:

```sh
sudo -u tadmin sqlite3 /var/lib/tadmin/tadmin.db ".backup '/var/lib/tadmin/tadmin-backup.db'"
sudo install -d -o root -g root -m 0700 /var/backups/tadmin/tadmin-YYYYMMDD
sudo install -o root -g root -m 0600 /var/lib/tadmin/tadmin-backup.db /var/backups/tadmin/tadmin-YYYYMMDD/tadmin.db
sudo rm /var/lib/tadmin/tadmin-backup.db
```

Store backups separately from the service account and protect them as sensitive operational data.

## Restore

Restore only while the service is stopped:

```sh
sudo systemctl stop tadmin
sudo install -d -o root -g root -m 0700 /var/backups/tadmin/pre-restore-YYYYMMDD
sudo mv /var/lib/tadmin/tadmin.db /var/backups/tadmin/pre-restore-YYYYMMDD/
if [ -f /var/lib/tadmin/tadmin.db-wal ]; then sudo mv /var/lib/tadmin/tadmin.db-wal /var/backups/tadmin/pre-restore-YYYYMMDD/; fi
if [ -f /var/lib/tadmin/tadmin.db-shm ]; then sudo mv /var/lib/tadmin/tadmin.db-shm /var/backups/tadmin/pre-restore-YYYYMMDD/; fi
sudo install -o tadmin -g tadmin -m 0600 /var/backups/tadmin/tadmin-YYYYMMDD/tadmin.db /var/lib/tadmin/tadmin.db
if [ -f /var/backups/tadmin/tadmin-YYYYMMDD/tadmin.db-wal ]; then sudo install -o tadmin -g tadmin -m 0600 /var/backups/tadmin/tadmin-YYYYMMDD/tadmin.db-wal /var/lib/tadmin/tadmin.db-wal; fi
if [ -f /var/backups/tadmin/tadmin-YYYYMMDD/tadmin.db-shm ]; then sudo install -o tadmin -g tadmin -m 0600 /var/backups/tadmin/tadmin-YYYYMMDD/tadmin.db-shm /var/lib/tadmin/tadmin.db-shm; fi
sudo systemctl start tadmin
curl --fail --silent http://127.0.0.1:8080/healthz
```

Keep the pre-restore snapshot until the restored service and Telegram flows have been checked. Moving the old database and sidecars out of the live path prevents an old WAL from being applied to the restored main file. If stale `-wal` or `-shm` files exist after an unclean shutdown, preserve them with the original database for investigation instead of deleting them blindly.

## Upgrade

1. Verify the downloaded artifact against `SHA256SUMS`.
2. Back up SQLite and the current binary.
3. Stop TAdmin.
4. Install the new binary as root, mode `0755`.
5. Start TAdmin. Embedded migrations run transactionally before Telegram polling begins.
6. Check `/healthz`, `systemctl status`, and recent logs.
7. Run `/tadmin status` and perform the release-specific acceptance checks.

Example binary replacement:

```sh
sha256sum -c SHA256SUMS
sudo systemctl stop tadmin
sudo cp /usr/local/bin/tadmin /usr/local/bin/tadmin.previous
sudo install -o root -g root -m 0755 tadmin-linux-amd64 /usr/local/bin/tadmin
sudo systemctl start tadmin
```

## Rollback

Database migrations are forward-only. Replacing only the binary is safe only when the older binary is explicitly documented as compatible with the migrated schema. Otherwise stop the service and restore both the previous binary and the database backup taken immediately before the upgrade.

Never use `git reset --hard`, delete the live database, or attempt schema surgery as an operational rollback.

## Bot Token rotation and invite links

Invite URLs are encrypted with an AES-256-GCM key derived in memory from the current Bot Token. A different Token cannot decrypt existing active links for later revocation.

Before a planned Token rotation:

1. list active links with `/tadmin invite list`;
2. record only their safe public IDs and names, then revoke every active TAdmin-managed link while the current Token can still decrypt it;
3. rotate the Token, update the service environment, and restart TAdmin;
4. create replacement links with the new Token.

After BotFather rotates a Token, the old Token can no longer authenticate Telegram API calls even though it is the key that matches old ciphertext. TAdmin therefore cannot automate revocation of those old records after an emergency rotation. Revoke the corresponding links manually through Telegram's administrator interface, then create replacements. Do not put either Token or an invite URL in logs or command arguments.

## Troubleshooting

- **Permission denied on SQLite:** `/var/lib/tadmin` must be owned by `tadmin:tadmin`, mode `0700`; the database should normally be mode `0600` under the unit's `UMask=0077`.
- **Service cannot read configuration:** `/etc/tadmin/config.toml` should be readable by group `tadmin`; `/etc/tadmin/tadmin.env` is root-owned mode `0600` and is read by systemd before privilege drop.
- **Mini App does not open:** check DNS, HTTPS certificate validity, the exact `public_base_url`, reverse-proxy path rules, and the BotFather Mini App domain.
- **Owner does not receive an invite:** the owner must first start the bot in private chat. TAdmin performs this preflight before creating a link.
- **Invite revocation remains pending:** confirm the bot still has `can_invite_users`, the Bot Token can decrypt the record, and Telegram is reachable. Known failures receive limited retries; a new owner revoke command can restart exhausted retries.
- **Management cards fail:** confirm the management chat is still a forum and the bot can manage topics and send/edit messages.
- **Telegram permission errors:** rerun the read-only permission checks described in [deployment.md](deployment.md), then use `/tadmin setup` only if onboarding state must be refreshed.
