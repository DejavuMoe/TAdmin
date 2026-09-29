// Package app assembles TAdmin's components and runs them until shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"tadmin/internal/bot"
	"tadmin/internal/channel"
	"tadmin/internal/config"
	"tadmin/internal/gate"
	"tadmin/internal/i18n"
	"tadmin/internal/intel"
	"tadmin/internal/preview"
	"tadmin/internal/preview/feeds"
	"tadmin/internal/roles"
	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/userbot"
	"tadmin/internal/webapp"
)

// Version is set at build time with -ldflags "-X tadmin/internal/app.Version=v2.x.y".
var Version = "dev"

// sessionStore persists the userbot session in the meta table.
type sessionStore struct{ st *store.Store }

func (s sessionStore) LoadSession(ctx context.Context) ([]byte, error) {
	return s.st.GetMeta(ctx, "userbot_session")
}

func (s sessionStore) StoreSession(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return s.st.SetMeta(ctx, "userbot_session", nil)
	}
	return s.st.SetMeta(ctx, "userbot_session", data)
}

// Run starts TAdmin and blocks until ctx is cancelled or a component fails.
func Run(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	started := time.Now()
	st, err := store.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	var opts []tg.Option
	if cfg.BotAPIURL != "" {
		opts = append(opts, tg.WithBaseURL(cfg.BotAPIURL))
	}
	client := tg.New(cfg.BotToken, opts...)
	me, err := getMe(ctx, client, log)
	if err != nil {
		return err
	}
	log.Info("connected to Telegram", "bot", me.Username, "id", me.ID, "version", Version)
	if wh, err := client.GetWebhookInfo(ctx); err == nil && wh.URL != "" {
		log.Warn("removing existing webhook so long polling can work", "url", wh.URL)
		if err := client.DeleteWebhook(ctx); err != nil {
			return fmt.Errorf("delete webhook: %w", err)
		}
	}

	rc := roles.New(client, cfg.OwnerID, 10*time.Minute)

	var ub *userbot.Manager
	if cfg.UserbotEnabled() {
		ub = userbot.New(userbot.Options{
			APIID: cfg.Userbot.APIID, APIHash: cfg.Userbot.APIHash, Logger: log.With("component", "userbot"),
			DeviceModel: "TAdmin", AppVersion: Version,
		}, sessionStore{st})
	}

	var assessor intel.Assessor
	if cfg.TypeSafeAPIKey != "" {
		assessor, err = intel.NewTypeSafe(intel.Options{APIKey: cfg.TypeSafeAPIKey})
		if err != nil {
			log.Warn("TypeSafe assessor disabled", "error", err)
			assessor = nil
		}
	}

	engine := gate.New(gate.Config{
		Store: st, TG: client, Roles: rc, Logger: log.With("component", "gate"), OwnerID: cfg.OwnerID,
		BotID: me.ID, BotUsername: me.Username, Lang: cfg.Language, WebURL: cfg.Web.PublicURL,
		Assessor: assessor, CAS: gate.NewCAS(),
	})
	channels := channel.New(st, client, log.With("component", "channel"))

	notify := func(ctx context.Context, text string) {
		_, _ = client.SendMessage(ctx, tg.SendMessageParams{ChatID: cfg.OwnerID, Text: text, DisableNotif: true})
	}
	pcfg := preview.Config{
		Store:         st,
		Fetcher:       feeds.NewFetcher(feeds.Options{AllowPrivate: cfg.Preview.AllowPrivate}),
		CheckInterval: cfg.Preview.CheckInterval.Duration,
		MinGap:        cfg.Preview.MinGap.Duration,
		DailyLimit:    cfg.Preview.DailyLimit,
		Location:      cfg.Location,
		Notify:        notify,
		Lang:          cfg.Language,
		Logger:        log.With("component", "preview"),
	}
	if ub != nil {
		pcfg.Refresher = ub // avoid a typed-nil interface when disabled
	}
	pv := preview.New(pcfg)

	b := bot.New(bot.Deps{
		Config: cfg, Store: st, TG: client, Roles: rc, Gate: engine, Channel: channels, Preview: pv, Userbot: ub,
		Me: me, Logger: log.With("component", "bot"), Version: Version, Started: started,
		AIReady: assessor != nil, WebReady: cfg.WebEnabled(),
	})
	b.RegisterCommands(ctx)

	srv := &http.Server{
		Addr: cfg.Web.Listen,
		Handler: webapp.Handler(webapp.Config{
			BotToken: cfg.BotToken, Engine: engine, Version: Version, EnableApp: cfg.WebEnabled(),
			Health: st.Ping, Logger: log.With("component", "web"),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	ln, err := net.Listen("tcp", cfg.Web.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Web.Listen, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	errc := make(chan error, 6)
	run := func(name string, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errc <- fmt.Errorf("%s panicked: %v", name, r)
					cancel()
				}
			}()
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				errc <- fmt.Errorf("%s: %w", name, err)
				cancel()
			}
		}()
	}
	run("gate", engine.Run)
	run("bot", b.Run)
	run("preview", pv.Run)
	if ub != nil {
		run("userbot", ub.Run)
	}
	run("http", func(ctx context.Context) error {
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			<-ctx.Done()
			sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = srv.Shutdown(sctx)
		}()
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		<-stopped // in-flight requests finish before the database closes
		return nil
	})

	announce(ctx, client, st, cfg, log, me)
	log.Info("TAdmin is running", "listen", cfg.Web.Listen, "mini_app", cfg.WebEnabled(), "userbot", ub != nil, "ai", assessor != nil)

	<-ctx.Done()
	wg.Wait()
	b.Wait(10 * time.Second)
	channels.Wait()
	close(errc)
	var errs []error
	for err := range errc {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// getMe retries transient failures and explains a bad token.
func getMe(ctx context.Context, client *tg.Client, log *slog.Logger) (*tg.User, error) {
	delay := 2 * time.Second
	for {
		me, err := client.GetMe(ctx)
		if err == nil {
			return me, nil
		}
		if apiErr, ok := tg.AsError(err); ok && (apiErr.Code == 401 || apiErr.Code == 404) {
			return nil, errors.New("Telegram rejected the bot token (401/404): check TADMIN_BOT_TOKEN from @BotFather")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Warn("cannot reach Telegram yet; retrying", "error", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, time.Minute)
	}
}

// announce greets the owner on startup (silently after the first run).
func announce(ctx context.Context, client *tg.Client, st *store.Store, cfg *config.Config, log *slog.Logger, me *tg.User) {
	chats, _ := st.ListChats(ctx, false)
	first, _ := st.GetMeta(ctx, "greeted")
	lang := cfg.Language
	var text string
	if first == nil {
		text = i18n.Pick(lang,
			"🎉 <b>TAdmin 已上线！</b>\n\n接下来只需两步：\n1️⃣ 把我拉进你的群，并设为管理员（开启“封禁用户”“删除消息”“置顶消息”“邀请用户”）\n2️⃣ 回到这里发送 /start 打开控制台\n\n默认会为新成员开启“仅本人可见”的浮层验证，群里不会刷屏。",
			"🎉 <b>TAdmin is online!</b>\n\nTwo steps left:\n1️⃣ Add me to your group as an administrator (Ban users, Delete messages, Pin messages, Invite users)\n2️⃣ Come back and send /start to open the control panel\n\nNewcomers get an overlay challenge only they can see — no clutter in your group.")
	} else {
		text = i18n.Pick(lang, "🟢 TAdmin %s 已启动，正在守护 %d 个群。", "🟢 TAdmin %s started, guarding %d groups.")
		text = fmt.Sprintf(text, tg.Esc(Version), len(chats))
	}
	_, err := client.SendMessage(ctx, tg.SendMessageParams{ChatID: cfg.OwnerID, Text: text, DisableNotif: first != nil,
		ReplyMarkup: tg.Keyboard(tg.Row(tg.CBStyle(i18n.T(lang, "btn_open_panel"), "p:home", tg.StylePrimary)))})
	if err != nil {
		if tg.IsForbidden(err) || strings.Contains(err.Error(), "chat not found") {
			log.Warn("cannot message the owner yet: open https://t.me/" + me.Username + " and press Start")
		} else {
			log.Warn("owner greeting failed", "error", err)
		}
		return
	}
	_ = st.SetMeta(ctx, "greeted", []byte("1"))
}
