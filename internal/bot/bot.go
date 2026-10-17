// Package bot routes Telegram updates to TAdmin's features and implements
// the private-chat control panel and group administrator commands.
package bot

import (
	"context"
	"hash/fnv"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"tadmin/internal/channel"
	"tadmin/internal/config"
	"tadmin/internal/gate"
	"tadmin/internal/preview"
	"tadmin/internal/roles"
	"tadmin/internal/store"
	"tadmin/internal/tg"
	"tadmin/internal/userbot"
)

// Deps wires the bot.
type Deps struct {
	Config   *config.Config
	Store    *store.Store
	TG       *tg.Client
	Roles    *roles.Cache
	Gate     *gate.Engine
	Channel  *channel.Service
	Preview  *preview.Service // nil-safe: always constructed, may be disabled
	Userbot  *userbot.Manager // nil when not configured
	Me       *tg.User
	Logger   *slog.Logger
	Version  string
	Started  time.Time
	AIReady  bool
	WebReady bool
}

// Bot is the update router.
type Bot struct {
	Deps
	cfg      *config.Config
	st       *store.Store
	tg       *tg.Client
	log      *slog.Logger
	lang     string
	sessions *sessions

	loginMu sync.Mutex
	qr      userbot.QRLogin
	runCtx  context.Context
	queue   serialQueue
	bg      sync.WaitGroup
	inline  bool // run private handlers synchronously (tests)
}

// New creates the bot.
func New(d Deps) *Bot {
	return &Bot{
		Deps: d, cfg: d.Config, st: d.Store, tg: d.TG, log: d.Logger, lang: d.Config.Language,
		sessions: newSessions(),
	}
}

const offsetKey = "update_offset"

// Run long-polls updates until ctx is cancelled. Each batch is processed in
// parallel shards (keeping per-user order) and the offset is committed only
// after the whole batch has been handled.
func (b *Bot) Run(ctx context.Context) error {
	b.runCtx = ctx
	var offset int64
	if v, _ := b.st.GetMeta(ctx, offsetKey); v != nil {
		offset, _ = strconv.ParseInt(string(v), 10, 64)
	}
	backoff := time.Second
	for ctx.Err() == nil {
		pctx, cancel := context.WithTimeout(ctx, 70*time.Second)
		ups, err := b.tg.GetUpdates(pctx, offset, 50*time.Second)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if apiErr, ok := tg.AsError(err); ok && apiErr.Code == 409 {
				b.log.Error("another process is polling this bot token (409 Conflict); stop it or remove its webhook")
				backoff = 30 * time.Second
			} else {
				b.log.Warn("getUpdates failed", "error", err, "retry_in", backoff)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		if len(ups) == 0 {
			continue
		}
		b.process(ctx, ups)
		offset = ups[len(ups)-1].UpdateID + 1
		if err := b.st.SetMeta(ctx, offsetKey, []byte(strconv.FormatInt(offset, 10))); err != nil {
			b.log.Error("save update offset", "error", err)
		}
	}
	return nil
}

const shards = 16

func (b *Bot) process(ctx context.Context, ups []tg.Update) {
	// Bot membership changes register or retire chats; apply them before the
	// rest of the batch so member updates see the chat they belong to.
	for i := range ups {
		if ups[i].MyChatMember != nil {
			b.handle(ctx, &ups[i])
		}
	}
	queues := make([][]tg.Update, shards)
	for _, u := range ups {
		if u.MyChatMember != nil {
			continue
		}
		k := shardKey(&u)
		h := fnv.New32a()
		h.Write([]byte(strconv.FormatInt(k, 10)))
		i := h.Sum32() % shards
		queues[i] = append(queues[i], u)
	}
	var wg sync.WaitGroup
	for _, q := range queues {
		if len(q) == 0 {
			continue
		}
		wg.Add(1)
		go func(q []tg.Update) {
			defer wg.Done()
			for i := range q {
				b.handle(ctx, &q[i])
			}
		}(q)
	}
	wg.Wait()
}

// shardKey keeps updates concerning the same user in order.
func shardKey(u *tg.Update) int64 {
	switch {
	case u.ChatMember != nil:
		return u.ChatMember.NewChatMember.User.ID
	case u.ChatJoinRequest != nil:
		return u.ChatJoinRequest.From.ID
	case u.CallbackQuery != nil:
		return u.CallbackQuery.From.ID
	case u.MyChatMember != nil:
		return u.MyChatMember.Chat.ID
	case u.Message != nil && u.Message.From != nil:
		return u.Message.From.ID
	case u.Message != nil:
		return u.Message.Chat.ID
	}
	return 0
}

func (b *Bot) handle(ctx context.Context, u *tg.Update) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("panic while handling update", "update", u.UpdateID, "panic", r)
		}
	}()
	hctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch {
	case u.MyChatMember != nil:
		b.onMyChatMember(hctx, u.MyChatMember)
	case u.ChatMember != nil:
		if chat := b.managed(hctx, u.ChatMember.Chat.ID); chat != nil {
			b.Gate.OnMemberUpdate(hctx, chat, u.ChatMember)
		}
	case u.ChatJoinRequest != nil:
		if chat := b.managed(hctx, u.ChatJoinRequest.Chat.ID); chat != nil {
			b.Gate.OnJoinRequest(hctx, chat, u.ChatJoinRequest)
		} else if u.ChatJoinRequest.QueryID != "" {
			_ = b.tg.AnswerJoinRequestQuery(hctx, u.ChatJoinRequest.QueryID, tg.JoinQueue)
		}
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		if cq.Message != nil && cq.Message.Chat.Type == tg.ChatPrivate && !strings.HasPrefix(cq.Data, "g:") && !strings.HasPrefix(cq.Data, "q:") {
			// Panel actions may be slow (feed discovery, backups); keep them
			// off the update barrier so admissions are never delayed.
			b.serial(cq.From.ID, func(ctx context.Context) { b.onCallback(ctx, cq) })
		} else {
			b.onCallback(hctx, cq)
		}
	case u.Message != nil:
		switch u.Message.Chat.Type {
		case tg.ChatPrivate:
			m := u.Message
			key := m.Chat.ID
			b.serial(key, func(ctx context.Context) { b.onPrivate(ctx, m) })
		case tg.ChatGroup, tg.ChatSupergroup:
			b.onGroupMessage(hctx, u.Message)
		}
	}
}

// managed returns the chat if it is managed (active or paused).
func (b *Bot) managed(ctx context.Context, chatID int64) *store.Chat {
	c, err := b.st.GetChat(ctx, chatID)
	if err != nil || (c.Status != store.ChatActive && c.Status != store.ChatPaused) {
		return nil
	}
	return c
}

// isOwner reports whether id is the owner.
func (b *Bot) isOwner(id int64) bool { return id == b.cfg.OwnerID }

// canManage reports whether userID may configure chatID.
func (b *Bot) canManage(ctx context.Context, chatID, userID int64) bool {
	if b.isOwner(userID) {
		return true
	}
	ok, err := b.Roles.CanModerate(ctx, chatID, userID)
	return err == nil && ok
}

// RegisterCommands publishes the command menus.
func (b *Bot) RegisterCommands(ctx context.Context) {
	for _, lang := range []string{"", "en"} {
		l := b.lang
		if lang == "en" {
			l = "en"
		}
		private, admins := CommandMenus(l)
		if err := b.tg.SetMyCommands(ctx, private, &tg.BotCommandScope{Type: "all_private_chats"}, lang); err != nil {
			b.log.Warn("set private commands", "error", err)
		}
		if err := b.tg.SetMyCommands(ctx, admins, &tg.BotCommandScope{Type: "all_chat_administrators"}, lang); err != nil {
			// Older servers may reject is_ephemeral; retry without it.
			for i := range admins {
				admins[i].IsEphemeral = false
			}
			if err := b.tg.SetMyCommands(ctx, admins, &tg.BotCommandScope{Type: "all_chat_administrators"}, lang); err != nil {
				b.log.Warn("set admin commands", "error", err)
			}
		}
		if err := b.tg.SetMyCommands(ctx, []tg.BotCommand{}, &tg.BotCommandScope{Type: "all_group_chats"}, lang); err != nil {
			b.log.Debug("clear group commands", "error", err)
		}
	}
}

// notifyOwner sends an HTML message to the owner.
func (b *Bot) notifyOwner(ctx context.Context, text string, kb *tg.InlineKeyboard) {
	if _, err := b.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: b.cfg.OwnerID, Text: text, ReplyMarkup: kb}); err != nil {
		b.log.Warn("notify owner", "error", err)
	}
}

// serialQueue runs private-chat work per user, in order, off the update path.
type serialQueue struct {
	mu      sync.Mutex
	pending map[int64][]func(context.Context)
}

// serial queues fn for user. With b.inline set (tests) it runs immediately.
func (b *Bot) serial(user int64, fn func(context.Context)) {
	run := func(fn func(context.Context)) {
		ctx, cancel := context.WithTimeout(b.baseCtx(), 3*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in private handler", "panic", r)
			}
		}()
		fn(ctx)
	}
	if b.inline {
		run(fn)
		return
	}
	q := &b.queue
	q.mu.Lock()
	if q.pending == nil {
		q.pending = map[int64][]func(context.Context){}
	}
	_, running := q.pending[user]
	q.pending[user] = append(q.pending[user], fn)
	q.mu.Unlock()
	if running {
		return
	}
	b.bg.Add(1)
	go func() {
		defer b.bg.Done()
		for {
			q.mu.Lock()
			list := q.pending[user]
			if len(list) == 0 {
				delete(q.pending, user)
				q.mu.Unlock()
				return
			}
			next := list[0]
			q.pending[user] = list[1:]
			q.mu.Unlock()
			run(next)
		}
	}()
}

// chatFor loads chatID if userID may configure it. Non-owners can only
// reach chats the owner has authorized (active or paused).
func (b *Bot) chatFor(ctx context.Context, chatID, userID int64) (*store.Chat, bool) {
	c, err := b.st.GetChat(ctx, chatID)
	if err != nil {
		return nil, false
	}
	if b.isOwner(userID) {
		return c, true
	}
	if c.Status != store.ChatActive && c.Status != store.ChatPaused {
		return nil, false
	}
	return c, b.canManage(ctx, chatID, userID)
}

// canPin reports whether userID may manage pins in chatID (owner, creator,
// or an admin holding the pin right), so the bot never amplifies rights.
func (b *Bot) canPin(ctx context.Context, chatID, userID int64) bool {
	if b.isOwner(userID) {
		return true
	}
	admins, err := b.Roles.Admins(ctx, chatID)
	if err != nil {
		return false
	}
	a, ok := admins[userID]
	return ok && (a.Status == tg.StatusCreator || (a.CanPinMessages != nil && *a.CanPinMessages))
}

// background runs slow work off the update path; it is cancelled on shutdown.
func (b *Bot) background(fn func(context.Context)) {
	if b.inline {
		fn(context.Background())
		return
	}
	b.bg.Add(1)
	go func() {
		defer b.bg.Done()
		ctx, cancel := context.WithTimeout(b.baseCtx(), 5*time.Minute)
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				b.log.Error("panic in background task", "panic", r)
			}
		}()
		fn(ctx)
	}()
}

// Wait blocks until background work finished or d elapsed.
func (b *Bot) Wait(d time.Duration) {
	done := make(chan struct{})
	go func() { b.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		b.log.Warn("background work still running at shutdown")
	}
}

// CommandMenus returns the private-chat and group-administrator command
// menus in lang. It is also exported for the generated documentation.
func CommandMenus(lang string) (private, admins []tg.BotCommand) {
	private = []tg.BotCommand{
		{Command: "start", Description: t(lang, "cmd_start")},
		{Command: "panel", Description: t(lang, "cmd_panel")},
		{Command: "refresh", Description: t(lang, "cmd_refresh")},
		{Command: "backup", Description: t(lang, "cmd_backup")},
		{Command: "help", Description: t(lang, "cmd_help")},
	}
	admins = []tg.BotCommand{
		{Command: "settings", Description: t(lang, "cmd_settings"), IsEphemeral: true},
		{Command: "status", Description: t(lang, "cmd_status"), IsEphemeral: true},
		{Command: "pass", Description: t(lang, "cmd_pass")},
		{Command: "kick", Description: t(lang, "cmd_kick")},
		{Command: "ban", Description: t(lang, "cmd_ban")},
		{Command: "mute", Description: t(lang, "cmd_mute")},
		{Command: "unmute", Description: t(lang, "cmd_unmute")},
		{Command: "unban", Description: t(lang, "cmd_unban")},
		{Command: "unpin", Description: t(lang, "cmd_unpin")},
	}
	return private, admins
}
