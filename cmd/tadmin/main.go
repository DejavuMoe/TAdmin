package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"tadmin/internal/app"
	"tadmin/internal/config"
	"tadmin/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("tadmin", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "config.toml", "path to the TOML configuration file")
	checkOffline := flags.Bool("check", false, "validate config and initialize/check SQLite without contacting Telegram")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tadmin: unexpected positional arguments")
		return 2
	}
	if *showVersion {
		fmt.Printf("TAdmin %s\n", app.Version)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("configuration error", "error", err)
		return 1
	}

	if *checkOffline {
		db, err := store.Open(context.Background(), cfg.DatabasePath)
		if err != nil {
			logger.Error("offline storage check failed", "error", err)
			return 1
		}
		err = db.CheckIntegrity(context.Background())
		closeErr := db.Close()
		if err != nil || closeErr != nil {
			logger.Error("offline storage check failed", "error", err, "close_error", closeErr)
			return 1
		}
		fmt.Println("Configuration and SQLite checks passed; Telegram was not contacted.")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, logger); err != nil {
		logger.Error("tadmin stopped with an error", "error", err)
		return 1
	}
	return 0
}
