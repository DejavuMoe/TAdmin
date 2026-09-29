// Command tadmin runs the TAdmin Telegram group guardian.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
	_ "time/tzdata" // scratch images have no zoneinfo

	"tadmin/internal/app"
	"tadmin/internal/config"
	"tadmin/internal/store"
)

const usage = `TAdmin — self-hosted Telegram group guardian

Usage:
  tadmin [run]    [-config config.toml]   start the bot (default)
  tadmin check    [-config config.toml]   validate configuration and database
  tadmin backup   [-config config.toml] <out.db>   write a consistent database snapshot
  tadmin healthcheck [-config config.toml]   probe the local /healthz endpoint (for containers)
  tadmin sample-config                    print an annotated config.toml
  tadmin version                          print the version

Configuration can come entirely from environment variables:
  TADMIN_BOT_TOKEN (required), TADMIN_OWNER_ID (required), TADMIN_DATA_DIR,
  TADMIN_PUBLIC_URL, TADMIN_API_ID, TADMIN_API_HASH, TADMIN_TYPESAFE_API_KEY
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := "run"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	configPath := fs.String("config", envOr("TADMIN_CONFIG", "config.toml"), "path to config.toml (optional)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch cmd {
	case "version", "-v", "--version":
		fmt.Printf("TAdmin %s (%s %s/%s)\n", app.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return 0
	case "sample-config":
		fmt.Print(config.Sample)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "run", "check", "backup", "healthcheck":
	default:
		fmt.Fprintf(os.Stderr, "tadmin: unknown command %q\n\n%s", cmd, usage)
		return 2
	}

	cfg, err := config.Load(*configPath, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tadmin:", err)
		return 1
	}
	log := newLogger(&cfg)

	switch cmd {
	case "check":
		return check(&cfg)
	case "healthcheck":
		return healthcheck(&cfg)
	case "backup":
		if fs.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: tadmin backup [-config config.toml] <out.db>")
			return 2
		}
		if err := store.BackupFile(context.Background(), cfg.DatabasePath(), fs.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "tadmin: backup failed:", err)
			return 1
		}
		fmt.Println("Backup written to", fs.Arg(0))
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, &cfg, log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("TAdmin stopped with an error", "error", err)
		return 1
	}
	log.Info("TAdmin stopped")
	return 0
}

func check(cfg *config.Config) int {
	fmt.Println("✓ configuration is valid")
	fmt.Printf("  owner id      %d\n", cfg.OwnerID)
	fmt.Printf("  database      %s\n", cfg.DatabasePath())
	fmt.Printf("  language      %s  (timezone %s)\n", cfg.Language, cfg.Location)
	fmt.Printf("  mini app      %s\n", onOff(cfg.WebEnabled(), cfg.Web.PublicURL))
	fmt.Printf("  http listen   %s\n", cfg.Web.Listen)
	fmt.Printf("  userbot       %s\n", onOff(cfg.UserbotEnabled(), "configured"))
	fmt.Printf("  AI assessment %s\n", onOff(cfg.TypeSafeAPIKey != "", "configured"))
	st, err := store.Open(context.Background(), cfg.DatabasePath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ database:", err)
		return 1
	}
	defer st.Close()
	if err := st.Check(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "✗ database:", err)
		return 1
	}
	fmt.Println("✓ database is healthy (Telegram was not contacted)")
	return 0
}

func onOff(on bool, detail string) string {
	if on {
		return "on  " + detail
	}
	return "off"
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}[cfg.LogLevel]
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, opts)
	if cfg.LogFormat == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

func healthcheck(cfg *config.Config) int {
	host, port, err := net.SplitHostPort(cfg.Web.Listen)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy: HTTP", resp.StatusCode)
		return 1
	}
	return 0
}
