package gate

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"tadmin/internal/i18n"
	"tadmin/internal/intel"
	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// Step bits stored in Gate.Steps.
const (
	stepAction     = 1 << iota // admit / remove / approve / decline
	stepCleanup                // delete or finalize verification messages
	stepWelcome                // welcome message
	stepRecord                 // statistics event
	stepRestricted             // (state new) member muted
	stepChecked                // (state new) CAS / AI checks done
	stepCaptured               // (state new) earlier restrictions saved in PrevPerms
)

const maxRetries = 8

func (e *Engine) step(ctx context.Context, g *store.Gate) {
	switch g.State {
	case store.StateNew:
		e.start(ctx, g)
	case store.StateChallenge:
		if !e.now().Before(g.Deadline) {
			e.resolve(ctx, g, store.OutcomeTimeout, 0)
		}
	case store.StateReview:
		if !e.now().Before(g.NextAt) {
			e.resolve(ctx, g, reviewTimeoutOutcome(g), 0)
		}
	case store.StateResolving:
		e.finish(ctx, g)
	}
}

// reviewTimeoutOutcome decides what happens when nobody reviewed a gate in
// time. A user who never received a challenge because of a bot-side
// problem is let in rather than punished for it.
func reviewTimeoutOutcome(g *store.Gate) string {
	if g.LastError == "undeliverable" {
		return store.OutcomeCancel
	}
	return store.OutcomeTimeout
}

// retry schedules another attempt with exponential back-off, returning
// false once the retry budget is exhausted.
func (e *Engine) retry(ctx context.Context, g *store.Gate, err error) bool {
	g.Retries++
	g.LastError = trimErr(err)
	if g.Retries > maxRetries {
		return false
	}
	backoff := time.Duration(1<<min(g.Retries, 8)) * time.Second
	if apiErr, ok := tg.AsError(err); ok && apiErr.RetryAfter > backoff {
		backoff = apiErr.RetryAfter
	}
	g.NextAt = e.now().Add(min(backoff, 5*time.Minute))
	if serr := e.st.SaveGate(ctx, g); serr != nil {
		e.log.Error("save gate", "gate", g.ID, "error", serr)
	}
	e.log.Warn("gate step failed; retrying", "gate", g.ID, "state", g.State, "retries", g.Retries, "error", err)
	return true
}

func trimErr(err error) string {
	if err == nil {
		return ""
	}
	return tg.Truncate(err.Error(), 300)
}

// restrictNow mutes a new direct entrant immediately (called from the
// update path); the scheduler continues with checks and delivery.
func (e *Engine) restrictNow(ctx context.Context, id int64) {
	unlock := e.lock(id)
	defer unlock()
	g, err := e.st.GetGate(ctx, id)
	if err != nil || g.State != store.StateNew || g.Source != store.SourceJoin || g.Done(stepRestricted) {
		return
	}
	chat, err := e.st.GetChat(ctx, g.ChatID)
	if err != nil {
		return
	}
	if e.restrictStep(ctx, chat, g) {
		g.NextAt = e.now()
		_ = e.st.SaveGate(ctx, g)
	}
}

// restrictStep mutes the member, first recording any restriction an
// administrator had applied. It reports whether the gate may proceed.
func (e *Engine) restrictStep(ctx context.Context, chat *store.Chat, g *store.Gate) bool {
	if !e.now().Before(g.Deadline) {
		e.resolve(ctx, g, store.OutcomeCancel, 0)
		return false
	}
	m, err := e.tg.GetChatMember(ctx, g.ChatID, g.UserID)
	if err != nil {
		if tg.IsPermanent(err) {
			e.resolve(ctx, g, store.OutcomeLeft, 0)
		} else if !e.retry(ctx, g, err) {
			e.resolve(ctx, g, store.OutcomeCancel, 0)
		}
		return false
	}
	if !m.IsPresent() {
		e.resolve(ctx, g, store.OutcomeLeft, 0)
		return false
	}
	if m.IsAdmin() {
		e.resolve(ctx, g, store.OutcomeCancel, 0)
		return false
	}
	if !g.Done(stepCaptured) {
		if m.Status == tg.StatusRestricted && !e.ownMute(ctx, g, m) {
			// Preserve restrictions an administrator applied earlier.
			data, _ := json.Marshal(m.ChatPermissions)
			g.PrevPerms, g.PrevUntil = string(data), m.UntilDate
		}
		// Persist before muting: after a crash the member would look muted
		// and our own mute must never be mistaken for an admin restriction.
		g.Steps |= stepCaptured
		if err := e.st.SaveGate(ctx, g); err != nil {
			e.log.Error("save gate", "gate", g.ID, "error", err)
			return false
		}
	}
	// The mute expires on its own shortly after the latest possible
	// decision, so a long outage can never leave someone muted forever.
	until := g.Deadline.Add(10 * time.Minute)
	if chat.Settings.Verify.OnFail == "review" {
		until = until.Add(time.Duration(chat.Settings.Verify.ReviewTTL) * time.Second)
	}
	g.MuteUntil = until.Unix()
	if err := e.st.SaveGate(ctx, g); err != nil {
		return false
	}
	if err := e.tg.RestrictChatMember(ctx, g.ChatID, g.UserID, tg.Muted(), until); err != nil {
		switch {
		case tg.IsGone(err):
			e.resolve(ctx, g, store.OutcomeLeft, 0)
		case tg.IsPermanent(err):
			e.permissionProblem(ctx, chat, "restrict", err)
			e.resolve(ctx, g, store.OutcomeCancel, 0)
		case !e.retry(ctx, g, err):
			e.resolve(ctx, g, store.OutcomeCancel, 0)
		}
		return false
	}
	g.Steps |= stepRestricted
	g.Retries, g.LastError = 0, ""
	return e.st.SaveGate(ctx, g) == nil
}

// ownMute matches the deadline of a previous TAdmin mute.
func (e *Engine) ownMute(ctx context.Context, g *store.Gate, m *tg.ChatMember) bool {
	if !permsAllFalse(m.ChatPermissions) {
		return false
	}
	last, err := e.st.LastClosedGate(ctx, g.ChatID, g.UserID)
	return err == nil && last.Done(stepRestricted) && last.MuteUntil > 0 && m.UntilDate == last.MuteUntil
}

func permsAllFalse(p tg.ChatPermissions) bool {
	for _, v := range []*bool{p.CanSendMessages, p.CanSendAudios, p.CanSendDocuments, p.CanSendPhotos, p.CanSendVideos,
		p.CanSendVideoNotes, p.CanSendVoiceNotes, p.CanSendPolls, p.CanSendOtherMessages, p.CanAddWebPagePreviews,
		p.CanReactToMessages, p.CanChangeInfo, p.CanInviteUsers, p.CanPinMessages, p.CanManageTopics} {
		if v != nil && *v {
			return false
		}
	}
	return true
}

// start restricts the newcomer, runs optional checks and delivers the challenge.
func (e *Engine) start(ctx context.Context, g *store.Gate) {
	if !e.now().Before(g.Deadline) {
		e.resolve(ctx, g, store.OutcomeCancel, 0)
		return
	}
	chat, err := e.st.GetChat(ctx, g.ChatID)
	if err != nil || chat.Status == store.ChatLeft {
		e.resolve(ctx, g, store.OutcomeCancel, 0)
		return
	}
	if g.Source == store.SourceJoin && !g.Done(stepRestricted) {
		if !e.restrictStep(ctx, chat, g) {
			return
		}
	}
	if !g.Done(stepChecked) {
		outcome := e.checks(ctx, chat, g)
		g.Steps |= stepChecked
		if outcome != "" {
			e.resolve(ctx, g, outcome, 0)
			return
		}
		if err := e.st.SaveGate(ctx, g); err != nil {
			e.log.Error("save gate", "gate", g.ID, "error", err)
			return
		}
	}
	if err := e.deliver(ctx, chat, g); err != nil {
		// Permanent failures (no right to post, closed topic...) will not
		// improve by retrying; hand the case to administrators right away.
		if tg.IsPermanent(err) || !e.retry(ctx, g, err) {
			e.log.Warn("challenge undeliverable", "gate", g.ID, "error", err)
			e.toReview(ctx, chat, g, "undeliverable")
		}
	}
}

// checks runs CAS and AI profile assessment; it returns a terminal outcome
// only for CAS-listed accounts. AI results only escalate.
func (e *Engine) checks(ctx context.Context, chat *store.Chat, g *store.Gate) string {
	v := chat.Settings.Verify
	if v.CAS && e.cfg.CAS != nil {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		listed, err := e.cfg.CAS.Listed(cctx, g.UserID)
		cancel()
		if err != nil {
			e.log.Debug("cas lookup failed", "error", err)
		} else if listed {
			return store.OutcomeCAS
		}
	}
	if v.AI != "off" && v.AI != "" && e.cfg.Assessor != nil {
		p := intel.Profile{UserID: g.UserID, Bio: g.UserBio, ViaRequest: g.Source != store.SourceJoin, ChatTitle: chat.Title}
		first, last := splitName(g.UserName)
		p.FirstName, p.LastName, p.Username = first, last, g.UserUsername
		ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if info, err := e.tg.GetChat(ictx, g.UserID); err == nil {
			p.Bio = info.Bio
			p.FirstName, p.LastName = info.FirstName, info.LastName
			has := info.Photo != nil
			p.HasPhoto = &has
		}
		a, err := e.cfg.Assessor.Assess(ictx, p)
		cancel()
		if err != nil {
			e.log.Warn("profile assessment failed", "error", err)
		} else {
			g.Risk, g.RiskReason = a.Risk, a.Reason
			if !a.HintOnly && v.AI == "review" && intel.Classify(a, intel.DefaultElevated, intel.DefaultHigh) == intel.LevelHigh {
				g.Flags |= store.FlagHighRisk
			}
		}
	}
	_ = e.st.SaveGate(ctx, g)
	return ""
}

func splitName(full string) (string, string) {
	for i, r := range full {
		if r == ' ' {
			return full[:i], full[i+1:]
		}
	}
	return full, ""
}

// resolve moves g to the resolving state with outcome and runs it.
func (e *Engine) resolve(ctx context.Context, g *store.Gate, outcome string, actor int64) {
	if g.State == store.StateResolving || g.State == store.StateClosed {
		return
	}
	g.State, g.Outcome, g.ActorID = store.StateResolving, outcome, actor
	g.NextAt, g.Retries, g.LastError = e.now(), 0, ""
	// Steps from the "new" phase are irrelevant from here on; keep the bits
	// so we know whether a restriction must be lifted.
	if err := e.st.SaveGate(ctx, g); err != nil {
		e.log.Error("save gate", "gate", g.ID, "error", err)
		return
	}
	e.log.Info("verification resolved", "gate", g.ID, "chat", g.ChatID, "user", g.UserID, "outcome", outcome, "actor", actor)
	e.finish(ctx, g)
}

// toReview hands the gate to administrators until the review deadline.
func (e *Engine) toReview(ctx context.Context, chat *store.Chat, g *store.Gate, reason string) {
	if !g.Open() || g.State == store.StateResolving {
		return
	}
	ttl := time.Duration(chat.Settings.Verify.ReviewTTL) * time.Second
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if g.Source == store.SourceRequest {
		// Join requests stay pending on Telegram's side; there is no rush.
		ttl = max(ttl, time.Hour)
	}
	if g.Source == store.SourceJoin && g.Done(stepRestricted) {
		// Never extend a restriction an administrator changed in the meantime.
		if m, err := e.tg.GetChatMember(ctx, g.ChatID, g.UserID); err == nil && g.MuteUntil > 0 &&
			m.Status == tg.StatusRestricted && m.UntilDate == g.MuteUntil && permsAllFalse(m.ChatPermissions) {
			previous := g.MuteUntil
			g.MuteUntil = e.now().Add(ttl + 10*time.Minute).Unix()
			if err := e.st.SaveGate(ctx, g); err == nil {
				if err := e.tg.RestrictChatMember(ctx, g.ChatID, g.UserID, tg.Muted(), time.Unix(g.MuteUntil, 0)); err != nil {
					g.MuteUntil = previous
				}
			}
		}
	}
	g.State, g.NextAt, g.Retries = store.StateReview, e.now().Add(ttl), 0
	g.LastError = reason
	_ = e.st.SaveGate(ctx, g)
	e.showReview(ctx, chat, g, reason)
}

// finish performs the resolve steps; each is idempotent and retried.
func (e *Engine) finish(ctx context.Context, g *store.Gate) {
	chat, err := e.st.GetChat(ctx, g.ChatID)
	if err != nil {
		chat = &store.Chat{ID: g.ChatID, Settings: store.DefaultSettings()}
	}
	if !g.Done(stepAction) {
		if err := e.act(ctx, chat, g); err != nil {
			if tg.IsPermanent(err) {
				e.permissionProblem(ctx, chat, g.Outcome, err)
				e.record(ctx, g.ChatID, store.EvActionFail, g.UserID, 0, 0, trimErr(err))
			} else if e.retry(ctx, g, err) {
				return
			} else {
				e.record(ctx, g.ChatID, store.EvActionFail, g.UserID, 0, 0, trimErr(err))
			}
		}
		g.Steps |= stepAction
		g.Retries = 0
		_ = e.st.SaveGate(ctx, g)
	}
	if !g.Done(stepCleanup) {
		e.cleanup(ctx, chat, g)
		g.Steps |= stepCleanup
		_ = e.st.SaveGate(ctx, g)
	}
	if !g.Done(stepWelcome) {
		if store.Positive(g.Outcome) {
			e.welcome(ctx, chat, g)
		}
		g.Steps |= stepWelcome
		_ = e.st.SaveGate(ctx, g)
	}
	if !g.Done(stepRecord) {
		e.recordOutcome(ctx, g)
		g.Steps |= stepRecord
	}
	g.State = store.StateClosed
	if err := e.st.SaveGate(ctx, g); err != nil {
		e.log.Error("close gate", "gate", g.ID, "error", err)
	}
}

// act performs the Telegram action for the outcome.
func (e *Engine) act(ctx context.Context, chat *store.Chat, g *store.Gate) error {
	switch g.Source {
	case store.SourceGuard:
		return e.actGuard(ctx, g)
	case store.SourceRequest:
		switch {
		case store.Positive(g.Outcome):
			return ignoreGone(e.tg.ApproveJoinRequest(ctx, g.ChatID, g.UserID))
		case g.Outcome == store.OutcomeLeft || g.Outcome == store.OutcomeCancel:
			return nil
		default:
			err := ignoreGone(e.tg.DeclineJoinRequest(ctx, g.ChatID, g.UserID))
			if g.Outcome == store.OutcomeBan && err == nil {
				err = e.tg.BanChatMember(ctx, g.ChatID, g.UserID, time.Time{}, false)
			}
			return err
		}
	}
	// Direct joins.
	switch g.Outcome {
	case store.OutcomePass, store.OutcomeApprove:
		if !g.Done(stepRestricted) {
			return nil
		}
		return ignoreGone(e.unrestrict(ctx, g))
	case store.OutcomeCancel:
		if !g.Done(stepRestricted) {
			return nil
		}
		// Cancel usually means the user became an admin; restricting an
		// admin fails harmlessly, so permanent errors are not problems.
		if err := e.unrestrict(ctx, g); err != nil && !tg.IsPermanent(err) {
			return err
		}
		return nil
	case store.OutcomeLeft:
		// Lift our mute so it does not linger into a later rejoin; the user
		// is gone, so failures are harmless.
		if g.Done(stepRestricted) {
			_ = e.unrestrict(ctx, g)
		}
		return nil
	case store.OutcomeBan:
		return ignoreGone(e.tg.BanChatMember(ctx, g.ChatID, g.UserID, time.Time{}, true))
	case store.OutcomeCAS:
		return ignoreGone(e.tg.BanChatMember(ctx, g.ChatID, g.UserID, e.now().Add(30*24*time.Hour), false))
	default: // fail, timeout, reject
		return ignoreGone(e.tg.BanChatMember(ctx, g.ChatID, g.UserID, e.now().Add(e.cooldown(ctx, chat, g)), false))
	}
}

// unrestrict lifts TAdmin's mute, restoring an earlier admin restriction.
func (e *Engine) unrestrict(ctx context.Context, g *store.Gate) error {
	m, err := e.tg.GetChatMember(ctx, g.ChatID, g.UserID)
	if err != nil {
		return err
	}
	// Legacy gates lack a recorded deadline; leave those mutes to expire.
	if g.MuteUntil == 0 || m.Status != tg.StatusRestricted || m.UntilDate != g.MuteUntil || !permsAllFalse(m.ChatPermissions) {
		return nil
	}
	if g.PrevPerms != "" {
		var perms tg.ChatPermissions
		if json.Unmarshal([]byte(g.PrevPerms), &perms) == nil {
			until := time.Time{}
			if g.PrevUntil > 0 {
				until = time.Unix(g.PrevUntil, 0)
				if !until.After(e.now().Add(30 * time.Second)) {
					// Telegram interprets deadlines under 30 seconds as forever.
					return e.tg.RestrictChatMember(ctx, g.ChatID, g.UserID, tg.AllPermissions(true), time.Time{})
				}
			}
			return e.tg.RestrictChatMember(ctx, g.ChatID, g.UserID, perms, until)
		}
	}
	// All permissions → member status; group defaults then apply.
	return e.tg.RestrictChatMember(ctx, g.ChatID, g.UserID, tg.AllPermissions(true), time.Time{})
}

// cooldown returns the removal ban length: 2m × 4^(n-1), capped at 24h,
// where n counts failures within 24h including this one. The floor stays
// well above Telegram's 30-second "forever" threshold even with clock skew.
func (e *Engine) cooldown(ctx context.Context, chat *store.Chat, g *store.Gate) time.Duration {
	if !chat.Settings.Verify.Cooldown || g.Outcome == store.OutcomeReject || g.Has(store.FlagHelp) {
		return MinBan
	}
	n, _ := e.st.RecentFailures(ctx, g.ChatID, g.UserID, e.now().Add(-24*time.Hour))
	return Cooldown(n + 1)
}

// MinBan is the shortest removal ban TAdmin issues.
const MinBan = 2 * time.Minute

// Cooldown computes the ban for the n-th failure within 24 hours.
func Cooldown(n int) time.Duration {
	d := MinBan
	for i := 1; i < n && d < 24*time.Hour; i++ {
		d *= 4
	}
	return min(d, 24*time.Hour)
}

func ignoreGone(err error) error {
	if err != nil && tg.IsGone(err) {
		return nil
	}
	return err
}

func (e *Engine) recordOutcome(ctx context.Context, g *store.Gate) {
	kind := map[string]string{
		store.OutcomePass: store.EvPass, store.OutcomeApprove: store.EvApprove, store.OutcomeFail: store.EvFail,
		store.OutcomeTimeout: store.EvTimeout, store.OutcomeReject: store.EvReject, store.OutcomeBan: store.EvBan,
		store.OutcomeLeft: store.EvLeft, store.OutcomeCAS: store.EvCAS,
	}[g.Outcome]
	if kind == "" {
		return
	}
	var value int64
	if g.Outcome == store.OutcomePass {
		value = e.now().Sub(g.CreatedAt).Milliseconds()
	}
	e.record(ctx, g.ChatID, kind, g.UserID, g.ActorID, value, g.Source)
}

// permissionProblem alerts the owner that the bot lacks a right.
func (e *Engine) permissionProblem(ctx context.Context, chat *store.Chat, action string, err error) {
	e.log.Error("telegram action rejected", "chat", chat.ID, "action", action, "error", err)
	e.alert(ctx, "perm:"+itoa(chat.ID)+":"+action,
		i18n.T(e.cfg.Lang, "perm_problem", tg.Esc(chat.Title), tg.Esc(action), tg.Esc(trimErr(err))),
		tg.Keyboard(tg.Row(tg.CB(i18n.T(e.cfg.Lang, "btn_check_perms"), "p:chk:"+itoa(chat.ID)))))
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

var errNotYours = errors.New("not your verification")

// actGuard resolves a Guard join request query. Queries expire, so the
// regular join request methods act as a fallback for late decisions.
func (e *Engine) actGuard(ctx context.Context, g *store.Gate) error {
	switch {
	case store.Positive(g.Outcome):
		if err := e.tg.AnswerJoinRequestQuery(ctx, g.QueryID, tg.JoinApprove); err == nil {
			return nil
		}
		return ignoreGone(e.tg.ApproveJoinRequest(ctx, g.ChatID, g.UserID))
	case g.Outcome == store.OutcomeLeft || g.Outcome == store.OutcomeCancel:
		_ = e.tg.AnswerJoinRequestQuery(ctx, g.QueryID, tg.JoinQueue)
		return nil
	default:
		if err := e.tg.AnswerJoinRequestQuery(ctx, g.QueryID, tg.JoinDecline); err != nil {
			if err := ignoreGone(e.tg.DeclineJoinRequest(ctx, g.ChatID, g.UserID)); err != nil {
				return err
			}
		}
		if g.Outcome == store.OutcomeBan {
			return e.tg.BanChatMember(ctx, g.ChatID, g.UserID, time.Time{}, false)
		}
		return nil
	}
}
