package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentExamplesMatchApplicationSurface(t *testing.T) {
	root := filepath.Join("..", "..")
	checks := map[string][]string{
		filepath.Join(root, "deploy", "systemd", "tadmin.service"): {
			"EnvironmentFile=/etc/tadmin/tadmin.env",
			"StateDirectory=tadmin",
			"ExecStart=/usr/local/bin/tadmin --config /etc/tadmin/config.toml",
			"ProtectSystem=strict",
			"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		},
		filepath.Join(root, "deploy", "nginx", "tadmin.conf"): {
			"/verify/", "/api/verify/", "127.0.0.1:8080", "X-Forwarded-Proto https", "access_log off",
		},
		filepath.Join(root, "deploy", "caddy", "Caddyfile"): {
			"/verify/*", "/api/verify/*", "127.0.0.1:8080", "respond 404", "output discard",
		},
		filepath.Join(root, "docs", "deployment.md"): {
			"database_path = \"/var/lib/tadmin/tadmin.db\"", "listen_address = \"127.0.0.1:8080\"",
			"public_base_url = \"https://verify.example.com\"", "docs/operations.md",
		},
		filepath.Join(root, "docs", "operations.md"): {
			"WAL mode", "sqlite3", "Bot Token rotation", "forward-only",
		},
		filepath.Join(root, "README.md"): {
			"deploy/systemd/tadmin.service", "deploy/nginx/tadmin.conf", "deploy/caddy/Caddyfile",
			"docs/deployment.md", "docs/operations.md",
		},
	}
	for path, markers := range checks {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, marker := range markers {
			if !strings.Contains(string(contents), marker) {
				t.Errorf("%s does not contain %q", path, marker)
			}
		}
	}
}
