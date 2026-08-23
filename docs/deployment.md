# TAdmin deployment

TAdmin is deployed as one CGO-free Linux binary. It listens only on loopback and requires an existing HTTPS reverse proxy or Cloudflare Tunnel for the Mini App routes. It does not require Docker, Kubernetes, Redis, PostgreSQL, or a separate Web server process.

## 1. Prepare the host

The examples use a dedicated non-login account, `/etc/tadmin` for read-only configuration, and `/var/lib/tadmin` for SQLite state:

```sh
sudo useradd --system --home-dir /var/lib/tadmin --shell /usr/sbin/nologin tadmin
sudo install -d -o root -g tadmin -m 0750 /etc/tadmin
sudo install -d -o tadmin -g tadmin -m 0700 /var/lib/tadmin
sudo install -o root -g root -m 0755 tadmin-linux-amd64 /usr/local/bin/tadmin
sudo install -o root -g tadmin -m 0640 config.example.toml /etc/tadmin/config.toml
sudo install -o root -g root -m 0644 deploy/systemd/tadmin.service /etc/systemd/system/tadmin.service
sudo install -o root -g root -m 0600 /dev/null /etc/tadmin/tadmin.env
```

Use `tadmin-linux-arm64` on an ARM64 host. Edit `/etc/tadmin/config.toml` so the deployment values include:

```toml
owner_user_id = 123456789
database_path = "/var/lib/tadmin/tadmin.db"
listen_address = "127.0.0.1:8080"
public_base_url = "https://verify.example.com"
management_chat_id = -1001234567890
default_language = "zh-CN"
```

The example identifiers are placeholders. `public_base_url` must be the final external HTTPS origin, without a query or fragment.

Write only the Bot Token to `/etc/tadmin/tadmin.env`:

```text
TADMIN_BOT_TOKEN=replace-with-the-real-token
```

Keep that file mode `0600`. Do not add the token to TOML, shell history, source control, service arguments, screenshots, tickets, or logs.

## 2. Install the service

The supplied [systemd unit](../deploy/systemd/tadmin.service) uses the actual TAdmin CLI, `--config /etc/tadmin/config.toml`. It creates a writable state directory while keeping the binary, configuration, home directories, and the rest of the filesystem protected.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now tadmin
sudo systemctl status tadmin
curl --fail --silent http://127.0.0.1:8080/healthz
```

If startup fails, use `sudo journalctl -u tadmin -n 100 --no-pager`. TAdmin emits JSON logs to standard output and never intentionally logs the Bot Token, complete Mini App `initData`, challenge answers, message bodies, or full invite links.

## 3. HTTPS reverse proxy

### Nginx

Copy [deploy/nginx/tadmin.conf](../deploy/nginx/tadmin.conf), replace `verify.example.com` and the certificate paths, validate it with `nginx -t`, then reload Nginx. The example exposes only `/verify/` and `/api/verify/`; `/healthz` remains loopback-only. Access logging is disabled because the route contains an opaque verification-session token.

### Caddy

Copy [deploy/caddy/Caddyfile](../deploy/caddy/Caddyfile), replace the hostname, run `caddy validate --config /etc/caddy/Caddyfile`, then reload Caddy. Caddy obtains and renews HTTPS certificates under its normal configuration. The example returns 404 for unrelated paths and discards access logs so session tokens are not retained.

After either proxy is live, verify only the public route shape—never paste a real verification session into logs or support messages:

```sh
curl --head https://verify.example.com/verify/not-a-real-session
```

An invalid session response is expected. A TLS, DNS, proxy, or connection error is not.

### Cloudflare Tunnel

Cloudflare Tunnel may point a hostname at `http://127.0.0.1:8080`; TAdmin still remains bound to loopback. Restrict the public hostname to `/verify/*` and `/api/verify/*` with Cloudflare rules, or point the tunnel at a local Nginx/Caddy listener that applies the supplied path allowlist. Block unrelated paths so `/healthz` remains local by default, and do not enable request-URI logging or analytics that retain verification-session paths. No Cloudflare SDK or credentials are required by TAdmin itself.

Do not enable an origin rewrite that changes the public hostname or scheme. Telegram must open exactly the HTTPS origin configured in `public_base_url`.

## 4. Telegram preparation

Before `/tadmin setup`, confirm that:

- the owner has started the bot in private chat, which is also required for private invite-link delivery;
- the bot is an administrator in each source supergroup with invite, restrict, and delete-message rights;
- the global management supergroup has forum topics enabled;
- the bot can manage topics and send/edit messages in the management group;
- the Mini App domain and `public_base_url` match the deployed HTTPS origin.

Run `/tadmin setup` in each source supergroup only after the service and HTTPS endpoint are healthy.

Operational procedures are in [docs/operations.md](operations.md).
