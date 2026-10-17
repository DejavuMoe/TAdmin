package gate

import (
	"context"
	"encoding/base64"
	"errors"
	"slices"
	"strconv"
	"strings"

	"tadmin/internal/captcha"
	"tadmin/internal/i18n"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// Result statuses shared by buttons and the Mini App.
const (
	StatusChallenge = "challenge"
	StatusPassed    = "passed"
	StatusFailed    = "failed"
	StatusReview    = "review"
	StatusExpired   = "expired"
	StatusClosed    = "closed"
)

// Web errors.
var (
	ErrInvalid   = errors.New("invalid")
	ErrForbidden = errors.New("forbidden")
	ErrExpired   = errors.New("expired")
	ErrStale     = errors.New("stale")
	ErrPending   = errors.New("pending")
)

// submit checks a full answer and advances the gate. It returns the new
// status and whether a new challenge generation was created.
func (e *Engine) submit(ctx context.Context, chat *store.Chat, g *store.Gate, answer []int) (string, bool) {
	ch, err := e.challenge(g)
	if err != nil {
		return StatusChallenge, false
	}
	if ch.Check(answer) {
		if g.Has(store.FlagHighRisk) {
			e.toReview(ctx, chat, g, "risk")
			return StatusReview, false
		}
		e.resolve(ctx, g, store.OutcomePass, 0)
		return StatusPassed, false
	}
	g.Attempts++
	g.Picks = ""
	if g.Attempts >= g.MaxAttempts {
		if chat.Settings.Verify.OnFail == "review" {
			e.toReview(ctx, chat, g, "failed")
			return StatusReview, false
		}
		e.resolve(ctx, g, store.OutcomeFail, 0)
		return StatusFailed, false
	}
	g.Gen++
	_ = e.st.SaveGate(ctx, g)
	return StatusChallenge, true
}

func viewOf(cq *tg.CallbackQuery) view {
	if cq.Message == nil {
		return view{}
	}
	return view{chat: cq.Message.Chat.ID, msg: cq.Message.MessageID, eph: cq.Message.EphemeralMessageID}
}

func (e *Engine) answer(ctx context.Context, cq *tg.CallbackQuery, text string, alert bool) {
	_ = e.tg.AnswerCallback(ctx, tg.AnswerCallbackParams{CallbackQueryID: cq.ID, Text: text, ShowAlert: alert})
}

// OnCallback handles gate buttons ("g:" and "gs:"). It reports whether the
// callback belonged to the engine.
func (e *Engine) OnCallback(ctx context.Context, cq *tg.CallbackQuery) bool {
	switch {
	case strings.HasPrefix(cq.Data, "gs:"):
		e.sharedStart(ctx, cq)
		return true
	case strings.HasPrefix(cq.Data, "g:"):
	default:
		return false
	}
	parts := strings.Split(cq.Data, ":")
	if len(parts) < 3 {
		e.answer(ctx, cq, "", false)
		return true
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		e.answer(ctx, cq, "", false)
		return true
	}
	action := parts[2]
	gen, cell := -1, -1
	if len(parts) > 3 {
		gen, _ = strconv.Atoi(parts[3])
	}
	if len(parts) > 4 {
		cell, _ = strconv.Atoi(parts[4])
	}

	unlock := e.lock(id)
	defer unlock()
	g, err := e.st.GetGate(ctx, id)
	lang := langOf(&cq.From, e.cfg.Lang)
	if err != nil {
		e.answer(ctx, cq, i18n.T(lang, "cb_gone"), false)
		return true
	}
	chat, err := e.st.GetChat(ctx, g.ChatID)
	if err != nil {
		e.answer(ctx, cq, i18n.T(lang, "cb_gone"), false)
		return true
	}

	switch action {
	case "a", "k", "b":
		e.adminButton(ctx, cq, chat, g, action)
		return true
	}
	if cq.From.ID != g.UserID {
		e.answer(ctx, cq, i18n.T(lang, "cb_not_yours"), true)
		return true
	}
	lang = g.Lang
	if !g.Answering() {
		switch g.State {
		case store.StateReview:
			e.answer(ctx, cq, i18n.T(lang, "cb_in_review"), false)
		default:
			e.answer(ctx, cq, i18n.T(lang, "cb_gone"), false)
		}
		return true
	}
	if !e.now().Before(g.Deadline) {
		e.answer(ctx, cq, i18n.T(lang, "cb_expired"), true)
		e.resolve(ctx, g, store.OutcomeTimeout, 0)
		return true
	}
	v := viewOf(cq)
	switch action {
	case "s":
		e.openOverlay(ctx, cq, g)
	case "h":
		g.Flags |= store.FlagHelp
		e.record(ctx, g.ChatID, store.EvHelp, g.UserID, 0, 0, "")
		e.answer(ctx, cq, i18n.T(lang, "cb_help_sent"), true)
		e.toReview(ctx, chat, g, "help")
	case "r":
		if gen != g.Gen {
			e.answer(ctx, cq, i18n.T(lang, "cb_stale"), false)
			_ = e.render(ctx, g, v, true, "")
			return true
		}
		g.Picks = ""
		_ = e.st.SaveGate(ctx, g)
		e.answer(ctx, cq, "", false)
		_ = e.render(ctx, g, v, false, "")
	case "p", "o":
		if gen != g.Gen {
			e.answer(ctx, cq, i18n.T(lang, "cb_stale"), false)
			_ = e.render(ctx, g, v, true, "")
			return true
		}
		var ans []int
		if action == "o" {
			ans = []int{cell}
		} else {
			if cell < 0 || cell >= captcha.GridSize*captcha.GridSize {
				e.answer(ctx, cq, "", false)
				return true
			}
			picks := parsePicks(g.Picks)
			if i := slices.Index(picks, cell); i >= 0 {
				picks = slices.Delete(picks, i, i+1) // tap again to undo
			} else {
				picks = append(picks, cell)
			}
			ch, err := e.challenge(g)
			if err != nil {
				e.answer(ctx, cq, "", false)
				return true
			}
			if len(picks) < ch.Picks {
				g.Picks = formatPicks(picks)
				_ = e.st.SaveGate(ctx, g)
				e.answer(ctx, cq, "", false)
				_ = e.render(ctx, g, v, false, "")
				return true
			}
			ans = picks
		}
		status, fresh := e.submit(ctx, chat, g, ans)
		switch status {
		case StatusPassed:
			e.answer(ctx, cq, i18n.T(lang, "cb_passed"), false)
		case StatusFailed:
			e.answer(ctx, cq, i18n.T(lang, "cb_failed"), true)
		case StatusReview:
			e.answer(ctx, cq, i18n.T(lang, "user_review"), true)
		default:
			e.answer(ctx, cq, i18n.T(lang, "cb_wrong", g.MaxAttempts-g.Attempts), false)
			if fresh {
				_ = e.render(ctx, g, v, true, i18n.T(lang, "cap_wrong_note"))
			}
		}
	default:
		e.answer(ctx, cq, "", false)
	}
	return true
}

// openOverlay shows the challenge as an ephemeral message in place of the
// prompt the user tapped; delivery to the tapping client is guaranteed.
func (e *Engine) openOverlay(ctx context.Context, cq *tg.CallbackQuery, g *store.Gate) {
	if g.Delivery == store.DeliveryPrivate {
		_ = e.tg.AnswerCallback(ctx, tg.AnswerCallbackParams{CallbackQueryID: cq.ID, URL: e.DeepLink(g)})
		return
	}
	eph := &tg.EphemeralParams{ReceiverUserID: g.UserID, CallbackQueryID: cq.ID}
	if cq.Message != nil && cq.Message.EphemeralMessageID == 0 {
		eph.ReplaceCallbackQueryMsg = true
	}
	m, err := e.sendChallenge(ctx, g, g.ChatID, eph, false, false)
	if err != nil {
		e.log.Info("ephemeral challenge failed; falling back to private chat", "gate", g.ID, "error", err)
		_ = e.tg.AnswerCallback(ctx, tg.AnswerCallbackParams{CallbackQueryID: cq.ID, URL: e.DeepLink(g)})
		return
	}
	_ = e.tg.AnswerCallback(ctx, tg.AnswerCallbackParams{CallbackQueryID: cq.ID})
	if m.EphemeralMessageID > 0 {
		if g.EphMsg > 0 && g.EphMsg != m.EphemeralMessageID {
			e.dropEphemeral(ctx, g, g.EphMsg)
		}
		g.EphMsg = m.EphemeralMessageID
		g.Flags |= store.FlagEphemeral
		_ = e.st.SaveGate(ctx, g)
		e.scheduleEphemeralCleanup(ctx, g, m.EphemeralMessageID)
	}
}

// sharedStart handles the lockdown notice button shared by all newcomers.
func (e *Engine) sharedStart(ctx context.Context, cq *tg.CallbackQuery) {
	chatID, _ := strconv.ParseInt(strings.TrimPrefix(cq.Data, "gs:"), 10, 64)
	g, err := e.st.OpenGateFor(ctx, chatID, cq.From.ID)
	if err != nil || !g.Answering() {
		e.answer(ctx, cq, i18n.T(langOf(&cq.From, e.cfg.Lang), "cb_no_need"), true)
		return
	}
	unlock := e.lock(g.ID)
	defer unlock()
	if g, err = e.st.GetGate(ctx, g.ID); err != nil || !g.Answering() {
		e.answer(ctx, cq, "", false)
		return
	}
	e.openOverlay(ctx, cq, g)
}

func (e *Engine) adminButton(ctx context.Context, cq *tg.CallbackQuery, chat *store.Chat, g *store.Gate, action string) {
	lang := langOf(&cq.From, e.cfg.Lang)
	ok, err := e.cfg.Roles.CanModerate(ctx, chat.ID, cq.From.ID)
	if err != nil || !ok {
		e.answer(ctx, cq, i18n.T(lang, "cb_admin_only"), true)
		return
	}
	outcome := map[string]string{"a": store.OutcomeApprove, "k": store.OutcomeReject, "b": store.OutcomeBan}[action]
	if err := e.decide(ctx, g, outcome, cq.From.ID); err != nil {
		e.answer(ctx, cq, i18n.T(lang, "cb_gone"), false)
		return
	}
	e.answer(ctx, cq, i18n.T(lang, "cb_done"), false)
}

// decide applies an administrator decision to an open gate (lock held).
func (e *Engine) decide(ctx context.Context, g *store.Gate, outcome string, actor int64) error {
	if !g.Open() || g.State == store.StateResolving {
		return ErrExpired
	}
	e.resolve(ctx, g, outcome, actor)
	return nil
}

// Decide applies an administrator decision by gate id after checking the
// actor's rights. Used by commands and the control panel.
func (e *Engine) Decide(ctx context.Context, gateID int64, outcome string, actor int64) error {
	unlock := e.lock(gateID)
	defer unlock()
	g, err := e.st.GetGate(ctx, gateID)
	if err != nil {
		return err
	}
	ok, err := e.cfg.Roles.CanModerate(ctx, g.ChatID, actor)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return e.decide(ctx, g, outcome, actor)
}

// DecideUser applies a decision to the open gate of (chat, user).
func (e *Engine) DecideUser(ctx context.Context, chatID, userID int64, outcome string, actor int64) error {
	g, err := e.st.OpenGateFor(ctx, chatID, userID)
	if err != nil {
		return err
	}
	return e.Decide(ctx, g.ID, outcome, actor)
}

// OnPrivateStart handles /start v<token> in a private chat.
func (e *Engine) OnPrivateStart(ctx context.Context, user *tg.User, token string) {
	lang := langOf(user, e.cfg.Lang)
	reply := func(key string, args ...any) {
		_, _ = e.tg.SendMessage(ctx, tg.SendMessageParams{ChatID: user.ID, Text: i18n.T(lang, key, args...)})
	}
	g, err := e.st.GateByToken(ctx, token)
	if err != nil {
		reply("pm_invalid")
		return
	}
	unlock := e.lock(g.ID)
	defer unlock()
	if g, err = e.st.GetGate(ctx, g.ID); err != nil {
		reply("pm_invalid")
		return
	}
	if g.UserID != user.ID {
		reply("pm_not_yours")
		return
	}
	lang = g.Lang
	switch {
	case g.State == store.StateReview:
		reply("user_review")
		return
	case !g.Answering() || !e.now().Before(g.Deadline):
		reply("pm_closed")
		return
	}
	if g.CaptchaChat > 0 && g.CaptchaMsg > 0 {
		_ = e.tg.DeleteMessage(ctx, g.CaptchaChat, g.CaptchaMsg)
	}
	m, err := e.sendChallenge(ctx, g, user.ID, nil, false, true)
	if err != nil {
		e.log.Warn("private challenge failed", "gate", g.ID, "error", err)
		return
	}
	g.CaptchaChat, g.CaptchaMsg = user.ID, m.MessageID
	g.Flags |= store.FlagPrivate
	_ = e.st.SaveGate(ctx, g)
}

func langOf(u *tg.User, fallback string) string { return userLang(u, fallback) }

// View is the Mini App JSON view (see docs/architecture.md).
type View struct {
	Status       string   `json:"status"`
	Lang         string   `json:"lang,omitempty"`
	ChatTitle    string   `json:"chat_title,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	Image        string   `json:"image,omitempty"`
	Options      []string `json:"options,omitempty"`
	Picks        int      `json:"picks,omitempty"`
	Gen          int      `json:"gen,omitempty"`
	AttemptsLeft int      `json:"attempts_left,omitempty"`
	ExpiresAt    int64    `json:"expires_at,omitempty"`
	ServerTime   int64    `json:"server_time,omitempty"`
	Help         bool     `json:"help,omitempty"`
}

// webGate loads and authorizes a gate for a Mini App request (lock held by caller).
func (e *Engine) webGate(ctx context.Context, token string, userID int64, queryID string) (*store.Gate, error) {
	g, err := e.st.GateByToken(ctx, token)
	if err != nil {
		return nil, ErrInvalid
	}
	if g.UserID != userID {
		return nil, ErrForbidden
	}
	if g.Source == store.SourceGuard && queryID != "" && queryID != g.QueryID {
		return nil, ErrForbidden
	}
	return g, nil
}

func (e *Engine) terminalView(g *store.Gate) *View {
	switch {
	case g.State == store.StateReview:
		return &View{Status: StatusReview, Lang: g.Lang}
	case store.Positive(g.Outcome):
		return &View{Status: StatusPassed, Lang: g.Lang}
	case g.Outcome == store.OutcomeTimeout:
		return &View{Status: StatusExpired, Lang: g.Lang}
	case g.Outcome == store.OutcomeFail || g.Outcome == store.OutcomeReject || g.Outcome == store.OutcomeBan:
		return &View{Status: StatusFailed, Lang: g.Lang}
	}
	return &View{Status: StatusClosed, Lang: g.Lang}
}

func (e *Engine) challengeView(ctx context.Context, g *store.Gate) (*View, error) {
	ch, err := e.challenge(g)
	if err != nil {
		return nil, err
	}
	v := &View{
		Status: StatusChallenge, Lang: g.Lang, ChatTitle: e.chatTitle(ctx, g.ChatID), Kind: string(ch.Kind),
		Prompt: ch.Prompt, Options: ch.Options, Picks: ch.Picks, Gen: g.Gen,
		AttemptsLeft: g.MaxAttempts - g.Attempts, ExpiresAt: g.Deadline.Unix(), ServerTime: e.now().Unix(),
		Help: true,
	}
	if ch.Image != nil {
		v.Image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(ch.Image)
	}
	return v, nil
}

func (e *Engine) withWebGate(ctx context.Context, token string, userID int64, queryID string, fn func(*store.Gate) (*View, error)) (*View, error) {
	g, err := e.st.GateByToken(ctx, token)
	if err != nil {
		return nil, ErrInvalid
	}
	unlock := e.lock(g.ID)
	defer unlock()
	g, err = e.webGate(ctx, token, userID, queryID)
	if err != nil {
		return nil, err
	}
	if g.State == store.StateNew {
		// The Mini App can open before the scheduler prepared the gate.
		e.start(ctx, g)
		if g.State == store.StateNew {
			return nil, ErrPending
		}
	}
	if g.Answering() && !e.now().Before(g.Deadline) {
		e.resolve(ctx, g, store.OutcomeTimeout, 0)
	}
	if !g.Answering() {
		return e.terminalView(g), nil
	}
	return fn(g)
}

// WebOpen returns the current challenge view.
func (e *Engine) WebOpen(ctx context.Context, token string, userID int64, queryID string) (*View, error) {
	return e.withWebGate(ctx, token, userID, queryID, func(g *store.Gate) (*View, error) {
		if !g.Has(store.FlagWebApp) {
			g.Flags |= store.FlagWebApp
			_ = e.st.SaveGate(ctx, g)
		}
		return e.challengeView(ctx, g)
	})
}

// WebAnswer submits a full answer from the Mini App.
func (e *Engine) WebAnswer(ctx context.Context, token string, userID int64, queryID string, gen int, answer []int) (*View, error) {
	return e.withWebGate(ctx, token, userID, queryID, func(g *store.Gate) (*View, error) {
		if gen != g.Gen {
			return nil, ErrStale
		}
		chat, err := e.st.GetChat(ctx, g.ChatID)
		if err != nil {
			return nil, ErrInvalid
		}
		status, fresh := e.submit(ctx, chat, g, answer)
		if status == StatusChallenge {
			if fresh {
				// Keep chat-side views in sync with the new generation.
				e.syncViews(ctx, g)
			}
			return e.challengeView(ctx, g)
		}
		return &View{Status: status, Lang: g.Lang}, nil
	})
}

// WebHelp requests manual review from the Mini App.
func (e *Engine) WebHelp(ctx context.Context, token string, userID int64, queryID string) (*View, error) {
	return e.withWebGate(ctx, token, userID, queryID, func(g *store.Gate) (*View, error) {
		chat, err := e.st.GetChat(ctx, g.ChatID)
		if err != nil {
			return nil, ErrInvalid
		}
		g.Flags |= store.FlagHelp
		e.record(ctx, g.ChatID, store.EvHelp, g.UserID, 0, 0, "webapp")
		e.toReview(ctx, chat, g, "help")
		return &View{Status: StatusReview, Lang: g.Lang}, nil
	})
}

// syncViews re-renders known chat views after a generation change.
func (e *Engine) syncViews(ctx context.Context, g *store.Gate) {
	if g.EphMsg > 0 {
		_ = e.render(ctx, g, view{chat: g.ChatID, eph: g.EphMsg}, true, "")
	}
	if g.CaptchaMsg > 0 {
		_ = e.render(ctx, g, view{chat: g.CaptchaChat, msg: g.CaptchaMsg}, true, "")
	}
}
