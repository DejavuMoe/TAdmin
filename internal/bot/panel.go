package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tadmin/internal/store"
	"tadmin/internal/tg"
)

func statusIcon(c *store.Chat) string {
	switch {
	case c.Status == store.ChatPending:
		return "🟡"
	case c.Status == store.ChatPaused:
		return "⏸"
	case c.Problems != "" && strings.Contains(c.Problems, "chk_restrict") || strings.Contains(c.Problems, "chk_admin") || strings.Contains(c.Problems, "chk_supergroup"):
		return "🔴"
	case c.InLockdown(time.Now()):
		return "🛡"
	}
	return "🟢"
}

// homeScreen is the owner's dashboard.
func (b *Bot) homeScreen(ctx context.Context) screen {
	lang := b.lang
	chats, _ := b.st.ListChats(ctx, false)
	days, _ := b.st.Stats(ctx, 0, 1, b.cfg.Location)
	var joins, passed, blocked int
	if len(days) > 0 {
		c := days[0].Counts
		joins = c[store.EvJoin]
		passed = c[store.EvPass] + c[store.EvApprove]
		blocked = c[store.EvFail] + c[store.EvTimeout] + c[store.EvReject] + c[store.EvBan] + c[store.EvCAS] + c[store.EvBotKicked]
	}
	open, _ := b.st.CountOpenGates(ctx)
	pending := 0
	for _, n := range open {
		pending += n
	}
	var sb strings.Builder
	sb.WriteString(t(lang, "home_title", tg.Esc(b.Me.FirstName)))
	sb.WriteString("\n\n")
	sb.WriteString(t(lang, "home_today", len(chats), joins, passed, blocked, pending))
	st := b.Preview.Status(ctx)
	sb.WriteString("\n")
	switch {
	case b.Userbot == nil:
		sb.WriteString(t(lang, "home_preview_off"))
	case !st.Ready:
		sb.WriteString(t(lang, "home_preview_login"))
	default:
		sb.WriteString(t(lang, "home_preview", st.Queue.Queued, st.Queue.DoneSince, st.Sources))
	}
	var hints []string
	for _, c := range chats {
		if c.Status == store.ChatPending {
			hints = append(hints, t(lang, "home_hint_pending", tg.Esc(c.Title)))
		} else if statusIcon(c) == "🔴" {
			hints = append(hints, t(lang, "home_hint_problem", tg.Esc(c.Title)))
		}
	}
	if len(chats) == 0 {
		hints = append(hints, t(lang, "home_hint_add", b.Me.Username))
	}
	if len(hints) > 0 {
		sb.WriteString("\n\n" + strings.Join(hints, "\n"))
	}
	addURL := "https://t.me/" + b.Me.Username + "?startgroup=tadmin&admin=restrict_members+delete_messages+pin_messages+invite_users"
	kb := tg.Keyboard(
		tg.Row(tg.CBStyle(t(lang, "btn_groups"), "p:groups", tg.StylePrimary), tg.CB(t(lang, "btn_stats"), "p:stats")),
		tg.Row(tg.CB(t(lang, "btn_preview"), "p:pv"), tg.CB(t(lang, "btn_system"), "p:sys")),
		tg.Row(tg.URLButton(t(lang, "btn_add_group"), addURL)),
	)
	return screen{text: sb.String(), kb: kb}
}

// groupsScreen lists the groups a user can manage.
func (b *Bot) groupsScreen(ctx context.Context, userID int64, lang string) screen {
	chats := b.manageableChats(ctx, userID)
	open, _ := b.st.CountOpenGates(ctx)
	var rows [][]tg.Button
	for _, c := range chats {
		label := statusIcon(c) + " " + tg.Truncate(c.Title, 28)
		if n := open[c.ID]; n > 0 {
			label += fmt.Sprintf(" · ⏳%d", n)
		}
		rows = append(rows, tg.Row(tg.CB(label, "p:g:"+itoa(c.ID))))
	}
	text := t(lang, "groups_title", len(chats))
	if len(chats) == 0 {
		text += "\n\n" + t(lang, "home_hint_add", b.Me.Username)
	} else {
		text += "\n\n" + t(lang, "groups_legend")
	}
	if b.isOwner(userID) {
		addURL := "https://t.me/" + b.Me.Username + "?startgroup=tadmin&admin=restrict_members+delete_messages+pin_messages+invite_users"
		rows = append(rows, tg.Row(tg.URLButton(t(lang, "btn_add_group"), addURL)), tg.Row(back(lang, "p:home")))
	}
	return screen{text: text, kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

func (b *Bot) deliveryName(lang, d string) string { return t(lang, "delivery_"+d) }
func (b *Bot) kindName(lang, k string) string     { return t(lang, "kind_"+k) }

// groupScreen is the overview of one group.
func (b *Bot) groupScreen(ctx context.Context, c *store.Chat, userID int64, lang string) screen {
	if c == nil {
		return screen{text: t(lang, "cb_expired_panel")}
	}
	s := c.Settings
	id := itoa(c.ID)
	var sb strings.Builder
	title := tg.Esc(c.Title)
	if c.Username != "" {
		title = `<a href="https://t.me/` + c.Username + `">` + title + `</a>`
	}
	sb.WriteString(t(lang, "group_title", title, c.ID))
	sb.WriteString("\n")
	status := t(lang, "status_"+c.Status)
	if c.InLockdown(time.Now()) {
		status += " · " + t(lang, "status_lockdown_until", c.LockdownUntil.In(b.cfg.Location).Format("15:04"))
	}
	sb.WriteString(t(lang, "group_status_line", status))
	if c.Problems != "" {
		var probs []string
		for _, p := range strings.Split(c.Problems, ",") {
			probs = append(probs, t(lang, p+"_short"))
		}
		sb.WriteString("\n" + t(lang, "group_problems", strings.Join(probs, "、")))
	}
	sb.WriteString("\n\n")
	if s.Verify.Enabled {
		sb.WriteString(t(lang, "group_verify_on", b.deliveryName(lang, s.Verify.Delivery), b.kindName(lang, s.Verify.Kind), humanSeconds(s.Verify.Timeout, lang), s.Verify.Attempts))
	} else {
		sb.WriteString(t(lang, "group_verify_off"))
	}
	sb.WriteString("\n" + t(lang, "group_welcome", onOff(lang, s.Welcome.Enabled)))
	sb.WriteString("\n" + t(lang, "group_channel", t(lang, "unpin_"+s.Channel.Unpin)))
	if s.Raid.Threshold > 0 {
		sb.WriteString("\n" + t(lang, "group_raid", s.Raid.Threshold))
	} else {
		sb.WriteString("\n" + t(lang, "group_raid_off"))
	}
	days, _ := b.st.Stats(ctx, c.ID, 7, b.cfg.Location)
	var joins, passed, blocked int
	var solve []int64
	for _, d := range days {
		joins += d.Counts[store.EvJoin]
		passed += d.Counts[store.EvPass] + d.Counts[store.EvApprove]
		blocked += d.Counts[store.EvFail] + d.Counts[store.EvTimeout] + d.Counts[store.EvReject] + d.Counts[store.EvBan] + d.Counts[store.EvCAS]
		solve = append(solve, d.SolveMS...)
	}
	sb.WriteString("\n\n" + t(lang, "group_week", joins, passed, blocked, fmt.Sprintf("%.1f", float64(median(solve))/1000)))
	open, _ := b.st.CountOpenGates(ctx)
	pending := open[c.ID]

	pauseLabel := t(lang, "btn_pause")
	if c.Status == store.ChatPaused {
		pauseLabel = t(lang, "btn_resume")
	}
	rows := [][]tg.Button{
		tg.Row(tg.CB(t(lang, "btn_verify"), "p:v:"+id), tg.CB(t(lang, "btn_welcome"), "p:w:"+id)),
		tg.Row(tg.CB(t(lang, "btn_channel"), "p:c:"+id), tg.CB(t(lang, "btn_clean"), "p:cl:"+id)),
		tg.Row(tg.CB(t(lang, "btn_security"), "p:sec:"+id), tg.CB(t(lang, "btn_stats"), "p:st:"+id)),
		tg.Row(tg.CB(fmt.Sprintf(t(lang, "btn_pending"), pending), "p:q:"+id), tg.CB(t(lang, "btn_check_perms"), "p:chk:"+id)),
		tg.Row(tg.CB(pauseLabel, "p:pause:"+id), tg.CB(t(lang, "btn_demo"), "p:demo:"+id)),
	}
	if c.Status == store.ChatPending && b.isOwner(userID) {
		rows = append([][]tg.Button{tg.Row(
			tg.CBStyle(t(lang, "btn_authorize"), "au:ok:"+id, tg.StyleSuccess),
			tg.CBStyle(t(lang, "btn_leave"), "au:no:"+id, tg.StyleDanger))}, rows...)
	}
	last := tg.Row(back(lang, "p:groups"))
	if b.isOwner(userID) {
		last = tg.Row(tg.CB(t(lang, "btn_remove_group"), "p:del:"+id), back(lang, "p:groups"))
	}
	rows = append(rows, last)
	return screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

// statsScreen shows 14-day statistics for a chat (nil = all chats).
func (b *Bot) statsScreen(ctx context.Context, c *store.Chat, lang string) screen {
	var chatID int64
	title := t(lang, "stats_all")
	backTo := "p:home"
	if c != nil {
		chatID, title, backTo = c.ID, tg.Esc(c.Title), "p:g:"+itoa(c.ID)
	}
	days, err := b.st.Stats(ctx, chatID, 14, b.cfg.Location)
	if err != nil {
		return screen{text: t(lang, "error_generic"), kb: tg.Keyboard(tg.Row(back(lang, backTo)))}
	}
	var joins, passes, blocks []int
	var tj, tp, tb, tl, th, tbot, tunpin int
	var solve []int64
	for _, d := range days {
		j := d.Counts[store.EvJoin]
		p := d.Counts[store.EvPass] + d.Counts[store.EvApprove]
		bl := d.Counts[store.EvFail] + d.Counts[store.EvTimeout] + d.Counts[store.EvReject] + d.Counts[store.EvBan] + d.Counts[store.EvCAS]
		joins, passes, blocks = append(joins, j), append(passes, p), append(blocks, bl)
		tj, tp, tb = tj+j, tp+p, tb+bl
		tl += d.Counts[store.EvLeft]
		th += d.Counts[store.EvHelp]
		tbot += d.Counts[store.EvBotKicked]
		tunpin += d.Counts[store.EvUnpin]
		solve = append(solve, d.SolveMS...)
	}
	var sb strings.Builder
	sb.WriteString(t(lang, "stats_title", title))
	sb.WriteString("\n\n<code>")
	sb.WriteString(t(lang, "stats_row_join") + " " + sparkline(joins) + "\n")
	sb.WriteString(t(lang, "stats_row_pass") + " " + sparkline(passes) + "\n")
	sb.WriteString(t(lang, "stats_row_block") + " " + sparkline(blocks))
	sb.WriteString("</code>\n")
	first, lastDay := days[0].Day, days[len(days)-1].Day
	sb.WriteString("<i>" + first.Format("01-02") + " → " + lastDay.Format("01-02") + "</i>\n\n")
	sb.WriteString(t(lang, "stats_totals", tj, tp, pct(tp, tj), tb, tl))
	if len(solve) > 0 {
		sb.WriteString("\n" + t(lang, "stats_solve", fmt.Sprintf("%.1f", float64(median(solve))/1000)))
	}
	if th > 0 || tbot > 0 || tunpin > 0 {
		sb.WriteString("\n" + t(lang, "stats_misc", th, tbot, tunpin))
	}
	if c == nil {
		// Per-group ranking.
		chats, _ := b.st.ListChats(ctx, false)
		type row struct {
			title string
			n     int
		}
		var rows []row
		for _, ch := range chats {
			n, _ := b.st.CountEvents(ctx, ch.ID, store.EvJoin, days[0].Day)
			rows = append(rows, row{ch.Title, n})
		}
		if len(rows) > 1 {
			sb.WriteString("\n\n" + t(lang, "stats_by_group"))
			for _, r := range rows {
				sb.WriteString(fmt.Sprintf("\n• %s — %d", tg.Esc(tg.Truncate(r.title, 24)), r.n))
			}
		}
	}
	return screen{text: sb.String(), kb: tg.Keyboard(tg.Row(back(lang, backTo)))}
}

// pendingScreen lists open verifications with quick decisions.
func (b *Bot) pendingScreen(ctx context.Context, c *store.Chat, lang string) screen {
	gates, _ := b.st.OpenGates(ctx, c.ID, 12)
	var sb strings.Builder
	sb.WriteString(t(lang, "pending_title", tg.Esc(c.Title), len(gates)))
	var rows [][]tg.Button
	now := time.Now()
	for _, g := range gates {
		state := t(lang, "gstate_"+g.State)
		left := g.Deadline.Sub(now)
		if g.State == store.StateReview {
			left = g.NextAt.Sub(now)
		}
		sb.WriteString(fmt.Sprintf("\n\n%s %s\n   %s · %s · %s", "👤", tg.MentionID(g.UserID, g.UserName), state,
			t(lang, "source_"+g.Source), humanDuration(max(left, 0)+time.Minute, lang)))
		if g.LastError != "" && g.State != store.StateReview {
			sb.WriteString("\n   ⚠️ " + tg.Esc(tg.Truncate(g.LastError, 80)))
		}
		name := tg.Truncate(g.UserName, 14)
		rows = append(rows, tg.Row(
			tg.CBStyle("✅ "+name, "q:"+itoa(g.ID)+":a", tg.StyleSuccess),
			tg.CBStyle("🚫", "q:"+itoa(g.ID)+":k", tg.StyleDanger),
			tg.CB("⛔", "q:"+itoa(g.ID)+":b"),
		))
	}
	if len(gates) == 0 {
		sb.WriteString("\n\n" + t(lang, "pending_empty"))
	}
	rows = append(rows, tg.Row(tg.CB(t(lang, "btn_refresh"), "p:q:"+itoa(c.ID)), back(lang, "p:g:"+itoa(c.ID))))
	return screen{text: sb.String(), kb: &tg.InlineKeyboard{InlineKeyboard: rows}}
}

func (b *Bot) onPending(ctx context.Context, cq *tg.CallbackQuery, gateID int64, action string, msgID int) {
	lang := b.userLang(&cq.From)
	g, err := b.st.GetGate(ctx, gateID)
	if err != nil {
		b.answer(ctx, cq, t(lang, "cb_gone_generic"), false)
		return
	}
	c, allowed := b.chatFor(ctx, g.ChatID, cq.From.ID)
	outcome, known := map[string]string{"a": store.OutcomeApprove, "k": store.OutcomeReject, "b": store.OutcomeBan}[action]
	if !allowed || !known {
		b.answer(ctx, cq, t(lang, "cb_admin_only_panel"), true)
		return
	}
	if err := b.Gate.Decide(ctx, gateID, outcome, cq.From.ID); err != nil {
		b.answer(ctx, cq, t(lang, "cb_gone_generic"), false)
	} else {
		b.answer(ctx, cq, t(lang, "cb_done_generic"), false)
	}
	b.show(ctx, cq.From.ID, msgID, b.pendingScreen(ctx, c, lang))
}
