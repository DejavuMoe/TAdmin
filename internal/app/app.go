package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tadmin/internal/config"
	"tadmin/internal/retention"
	"tadmin/internal/store"
	"tadmin/internal/telegram"
	"tadmin/internal/verify"
	"tadmin/internal/webapp"
)

func Run(parent context.Context, cfg config.Config, logger *slog.Logger) error {
	startedAt := time.Now()
	database, err := store.Open(parent, cfg.DatabasePath)
	if err != nil {
		return err
	}

	verificationService := verify.NewService(database, cfg.BotToken)
	telegramClient, err := telegram.New(parent, cfg, database, verificationService, logger, Version, startedAt)
	if err != nil {
		_ = database.Close()
		return err
	}

	runContext, cancel := context.WithCancel(parent)
	defer cancel()
	retentionRunner := retention.New(database, logger)
	verificationHandler := webapp.New(verificationService, telegramClient.NotifyVerificationTerminal)

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           newHTTPHandler(database, verificationHandler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          log.New(&slogWriter{logger: logger}, "", 0),
	}

	telegramDone := make(chan struct{})
	retentionDone := make(chan struct{})
	go func() {
		defer close(telegramDone)
		telegramClient.Start(runContext)
	}()
	go func() {
		defer close(retentionDone)
		retentionRunner.Run(runContext)
	}()

	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			serverErrors <- nil
			return
		}
		serverErrors <- err
	}()

	logger.Info("tadmin started", "version", Version, "listen_address", cfg.ListenAddress)

	var runErr error
	select {
	case <-parent.Done():
	case err := <-serverErrors:
		if err != nil {
			runErr = fmt.Errorf("serve HTTP: %w", err)
		}
	}

	cancel()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	shutdownErr := server.Shutdown(shutdownContext)
	<-telegramDone
	<-retentionDone
	telegramClient.Close()
	closeErr := database.Close()

	logger.Info("tadmin stopped")
	return errors.Join(runErr, shutdownErr, closeErr)
}

type slogWriter struct {
	logger *slog.Logger
}

func (w *slogWriter) Write(data []byte) (int, error) {
	w.logger.Error("http server error", "message", strings.TrimSpace(string(data)))
	return len(data), nil
}
