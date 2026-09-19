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
	"tadmin/internal/join"
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
	handler     *commandHandler
	database    *store.Store
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
	return newClient(ctx, cfg, database, verification, logger, version, startedAt, telegramAPIBaseURL, newHTTPClient())
}

// The private constructor lets integration tests use an in-process Bot API.
// Production never reads an API override from configuration or environment.
func newClient(ctx context.Context, cfg config.Config, database *store.Store, verification *verify.Service, logger *slog.Logger, version string, startedAt time.Time, apiBase string, httpClient *http.Client) (*Client, error) {

	getMeContext, cancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer cancel()
	info, err := getMeAt(getMeContext, httpClient, cfg.BotToken, version, apiBase)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, err
	}

	if err := database.BindBot(ctx, info.ID); err != nil {
		httpClient.CloseIdleConnections()
		return nil, err
	}

	var handler *commandHandler
	bot, err := telegrambot.New(
		cfg.BotToken,
		telegrambot.WithSkipGetMe(),
		telegrambot.WithServerURL(apiBase),
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
	raw := &rawBotAPI{baseURL: apiBase, token: cfg.BotToken, version: version, httpClient: httpClient}
	webhookContext, webhookCancel := context.WithTimeout(ctx, apiRequestTimeout)
	defer webhookCancel()
	webhook, err := bot.GetWebhookInfo(webhookContext)
	if err != nil || webhook == nil {
		httpClient.CloseIdleConnections()
		return nil, fmt.Errorf("check webhook before polling: %s", sanitizeError(err, cfg.BotToken))
	}
	if webhook.URL != "" {
		httpClient.CloseIdleConnections()
		return nil, fmt.Errorf("this bot has an active webhook; remove it before using Long Polling")
	}
	adapter := &gateway{bot: bot, raw: raw, token: cfg.BotToken}
	coordinator := join.New(database, verification, adapter, logger, join.Options{
		OwnerUserID: cfg.OwnerUserID, ManagementChatID: cfg.ManagementChatID,
		PublicBaseURL: cfg.PublicBaseURL, BotUsername: info.Username,
	})
	handler = newCommandHandler(cfg, database, logger, info, version, startedAt, coordinator)

	logger.Info("telegram bot authenticated",
		"bot_id", info.ID,
		"username", info.Username,
		"supports_join_request_queries", info.SupportsJoinRequestQueries,
	)

	return &Client{
		bot: bot, httpClient: httpClient, raw: raw, coordinator: coordinator, handler: handler, database: database,
		logger: logger, token: cfg.BotToken,
	}, nil
}

func (c *Client) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	coordinatorDone := make(chan struct{})
	caughtUp := make(chan struct{})
	go func() {
		defer close(coordinatorDone)
		select {
		case <-ctx.Done():
			return
		case <-caughtUp:
		}
		c.coordinator.Run(ctx)
	}()
	// Drain retained membership changes before replaying old external actions.
	err := c.poll(ctx, caughtUp)
	cancel()
	<-coordinatorDone
	return err
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
		Transport:     &limitedTransport{base: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       70 * time.Second,
	}
}

func getMe(ctx context.Context, client *http.Client, token, version string) (BotInfo, error) {
	return getMeAt(ctx, client, token, version, telegramAPIBaseURL)
}

func getMeAt(ctx context.Context, client *http.Client, token, version, apiBase string) (BotInfo, error) {
	requestURL := apiBase + "/bot" + token + "/getMe"
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
		return BotInfo{}, fmt.Errorf("Telegram getMe failed with code %d: %s", envelope.ErrorCode, strings.ReplaceAll(envelope.Description, token, "[REDACTED]"))
	}
	if !envelope.Result.IsBot || envelope.Result.ID <= 0 || envelope.Result.Username == "" {
		return BotInfo{}, fmt.Errorf("Telegram getMe returned an invalid bot identity")
	}
	return envelope.Result, nil
}

func sanitizeError(err error, token string) string {
	if err == nil {
		return ""
	}
	// The pinned SDK includes the raw response body in JSON decode errors.
	// It can contain private verification links, so never forward that body.
	if strings.Contains(err.Error(), "error decode response body for method") {
		return "Telegram response could not be decoded"
	}
	if token == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), token, "[REDACTED]")
}
