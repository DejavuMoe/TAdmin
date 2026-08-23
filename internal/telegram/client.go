package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	telegrambot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"tadmin/internal/config"
	"tadmin/internal/invite"
	"tadmin/internal/join"
	"tadmin/internal/moderation"
	"tadmin/internal/store"
	"tadmin/internal/verify"
)

const (
	telegramAPIBaseURL = "https://api.telegram.org"
	longPollTimeout    = 60 * time.Second
	apiRequestTimeout  = 10 * time.Second
)

var allowedUpdates = telegrambot.AllowedUpdates{
	models.AllowedUpdateMessage,
	models.AllowedUpdateEditedMessage,
	models.AllowedUpdateCallbackQuery,
	models.AllowedUpdateChatMember,
	models.AllowedUpdateMyChatMember,
	models.AllowedUpdateChatJoinRequest,
}

type BotInfo struct {
	ID                         int64  `json:"id"`
	IsBot                      bool   `json:"is_bot"`
	Username                   string `json:"username"`
	SupportsJoinRequestQueries bool   `json:"supports_join_request_queries"`
}

type Client struct {
	bot         *telegrambot.Bot
	httpClient  *http.Client
	raw         *rawBotAPI
	coordinator *join.Coordinator
	moderation  *moderation.Coordinator
	invitation  *invite.Manager
	logger      *slog.Logger
	token       string
}

func New(
	ctx context.Context,
	cfg config.Config,
	database *store.Store,
	verification *verify.Service,
	logger *slog.Logger,
	version string,
	startedAt time.Time,
) (*Client, error) {
	httpClient := newHTTPClient()

	getMeContext, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	info, err := getMe(getMeContext, httpClient, cfg.BotToken, version)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, err
	}

	var handler *commandHandler
	bot, err := telegrambot.New(
		cfg.BotToken,
		telegrambot.WithSkipGetMe(),
		telegrambot.WithAllowedUpdates(allowedUpdates),
		telegrambot.WithDefaultHandler(func(ctx context.Context, bot *telegrambot.Bot, update *models.Update) {
			if handler != nil {
				handler.handle(ctx, bot, update)
			}
		}),
		telegrambot.WithHTTPClient(longPollTimeout, httpClient),
		telegrambot.WithErrorsHandler(func(err error) {
			logger.Error("telegram polling error", "error", sanitizeError(err, cfg.BotToken))
		}),
	)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, fmt.Errorf("initialize Telegram bot: %w", err)
	}
	raw := &rawBotAPI{baseURL: telegramAPIBaseURL, token: cfg.BotToken, version: version, httpClient: httpClient}
	adapter := &gateway{bot: bot, raw: raw, token: cfg.BotToken}
	coordinator := join.New(database, verification, adapter, logger, join.Options{
		OwnerUserID: cfg.OwnerUserID, ManagementChatID: cfg.ManagementChatID,
		PublicBaseURL: cfg.PublicBaseURL, BotUsername: info.Username,
	})
	moderationCoordinator := moderation.New(database, adapter, logger, moderation.Options{
		OwnerUserID: cfg.OwnerUserID, BotUserID: info.ID,
		ManagementChatID: cfg.ManagementChatID, BotToken: cfg.BotToken,
	})
	invitationManager, err := invite.New(database, adapter, logger, invite.Options{
		OwnerUserID: cfg.OwnerUserID, BotToken: cfg.BotToken,
	})
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, fmt.Errorf("initialize invite manager: %w", err)
	}
	handler = newCommandHandler(cfg, database, logger, info, version, startedAt,
		coordinator, moderationCoordinator, invitationManager)

	logger.Info("telegram bot authenticated",
		"bot_id", info.ID,
		"username", info.Username,
		"supports_join_request_queries", info.SupportsJoinRequestQueries,
	)

	return &Client{
		bot: bot, httpClient: httpClient, raw: raw, coordinator: coordinator, moderation: moderationCoordinator,
		invitation: invitationManager,
		logger:     logger, token: cfg.BotToken,
	}, nil
}

func (c *Client) Start(ctx context.Context) {
	coordinatorDone := make(chan struct{})
	moderationDone := make(chan struct{})
	invitationDone := make(chan struct{})
	go func() {
		defer close(coordinatorDone)
		c.coordinator.Run(ctx)
	}()
	go func() {
		defer close(moderationDone)
		c.moderation.Run(ctx)
	}()
	go func() {
		defer close(invitationDone)
		c.invitation.Run(ctx)
	}()
	c.poll(ctx)
	<-coordinatorDone
	<-moderationDone
	<-invitationDone
}

func (c *Client) NotifyVerificationTerminal() {
	c.coordinator.Wake()
}

func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 20
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 65 * time.Second

	return &http.Client{
		Transport: transport,
		Timeout:   70 * time.Second,
	}
}

func getMe(ctx context.Context, client *http.Client, token, version string) (BotInfo, error) {
	requestURL := telegramAPIBaseURL + "/bot" + token + "/getMe"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, http.NoBody)
	if err != nil {
		return BotInfo{}, fmt.Errorf("create Telegram getMe request: %s", sanitizeError(err, token))
	}
	req.Header.Set("User-Agent", "TAdmin/"+version)

	resp, err := client.Do(req)
	if err != nil {
		return BotInfo{}, fmt.Errorf("call Telegram getMe: %s", sanitizeError(err, token))
	}
	defer resp.Body.Close()

	var envelope struct {
		OK          bool    `json:"ok"`
		Result      BotInfo `json:"result"`
		Description string  `json:"description"`
		ErrorCode   int     `json:"error_code"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return BotInfo{}, fmt.Errorf("decode Telegram getMe response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || !envelope.OK {
		return BotInfo{}, fmt.Errorf("Telegram getMe failed with code %d: %s", envelope.ErrorCode, envelope.Description)
	}
	if !envelope.Result.IsBot || envelope.Result.ID <= 0 {
		return BotInfo{}, fmt.Errorf("Telegram getMe returned an invalid bot identity")
	}
	return envelope.Result, nil
}

func sanitizeError(err error, token string) string {
	if err == nil {
		return ""
	}
	if token == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), token, "[REDACTED]")
}
