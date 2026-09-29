// Package channel implements linked-channel conveniences for discussion
// groups: automatic unpinning of channel posts, one-tap cleanup of historic
// channel pins that preserves administrator pins, and blocking members who
// speak under the identity of an unrelated channel.
package channel

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tadmin/internal/store"
	"tadmin/internal/tg"
)

// Unpin modes.
const (
	UnpinOff        = "off"
	UnpinAll        = "unpin"
	UnpinKeepLatest = "keep_latest"
)

// Service handles channel features.
type Service struct {
	st  *store.Store
	tg  *tg.Client
	log *slog.Logger
	// delay before unpinning: Telegram applies the automatic pin right
	// after delivering the forward, so unpinning immediately can race.
	delay time.Duration
	wg    sync.WaitGroup
}

// New creates a service.
func New(st *store.Store, client *tg.Client, log *slog.Logger) *Service {
	return &Service{st: st, tg: client, log: log, delay: 2 * time.Second}
}

// SetDelay overrides the unpin delay (tests).
func (s *Service) SetDelay(d time.Duration) { s.delay = d }

// OnAutoForward handles an automatic forward from the linked channel.
func (s *Service) OnAutoForward(ctx context.Context, chat *store.Chat, msg *tg.Message) {
	if msg.SenderChat == nil {
		return
	}
	if chat.LinkedChatID == 0 {
		chat.LinkedChatID = msg.SenderChat.ID
		_ = s.st.SetChatInfo(ctx, chat.ID, chat.Title, chat.Username, msg.SenderChat.ID)
	}
	prev, _ := s.st.ChannelPins(ctx, chat.ID, 1)
	if err := s.st.AddChannelPin(ctx, chat.ID, msg.MessageID, msg.SenderChat.ID); err != nil {
		s.log.Warn("record channel pin", "chat", chat.ID, "error", err)
	}
	mode := chat.Settings.Channel.Unpin
	if mode != UnpinAll && mode != UnpinKeepLatest {
		return
	}
	target := msg.MessageID
	if mode == UnpinKeepLatest {
		if len(prev) == 0 || prev[0] == msg.MessageID {
			return
		}
		target = prev[0]
	}
	// Detach from the update handler context, which ends when handling returns.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		t := time.NewTimer(s.delay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for attempt := 0; attempt < 3; attempt++ {
			err := s.tg.UnpinChatMessage(ctx, chat.ID, target)
			if err == nil {
				_ = s.st.AddEvent(ctx, store.Event{ChatID: chat.ID, Kind: store.EvUnpin, Value: 1})
				return
			}
			if tg.IsForbidden(err) || (tg.IsPermanent(err) && !tg.IsGone(err)) {
				s.log.Warn("auto unpin failed", "chat", chat.ID, "error", err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt+2) * time.Second):
			}
		}
	}()
}

// CleanResult summarises CleanPins.
type CleanResult struct {
	Unpinned   int
	StoppedAt  string // "none" (no pins left) | "admin" (an admin pin is on top) | "limit"
	TopMessage int
}

// CleanPins unpins every channel post pinned in the discussion group while
// keeping administrator pins: first all recorded forwards, then repeatedly
// the current top pin while it comes from the linked channel.
func (s *Service) CleanPins(ctx context.Context, chat *store.Chat) (CleanResult, error) {
	var r CleanResult
	ids, err := s.st.ChannelPins(ctx, chat.ID, 1000)
	if err != nil {
		return r, err
	}
	var done []int
	for _, id := range ids {
		err := s.tg.UnpinChatMessage(ctx, chat.ID, id)
		switch {
		case err == nil:
			r.Unpinned++
			done = append(done, id)
		case tg.IsGone(err) || tg.IsPermanent(err) && !tg.IsForbidden(err):
			done = append(done, id)
		case tg.IsForbidden(err):
			return r, err
		}
	}
	if len(done) > 0 {
		_ = s.st.DeleteChannelPins(ctx, chat.ID, done)
	}
	for i := 0; i < 300; i++ {
		info, err := s.tg.GetChat(ctx, chat.ID)
		if err != nil {
			return r, err
		}
		pm := info.PinnedMessage
		if pm == nil {
			r.StoppedAt = "none"
			break
		}
		fromChannel := pm.IsAutomaticForward || (pm.SenderChat != nil && pm.SenderChat.ID != chat.ID &&
			(chat.LinkedChatID == 0 || pm.SenderChat.ID == chat.LinkedChatID))
		if !fromChannel {
			r.StoppedAt, r.TopMessage = "admin", pm.MessageID
			break
		}
		if err := s.tg.UnpinChatMessage(ctx, chat.ID, pm.MessageID); err != nil && !tg.IsGone(err) {
			return r, err
		}
		r.Unpinned++
		if i == 299 {
			r.StoppedAt = "limit"
		}
	}
	if r.Unpinned > 0 {
		_ = s.st.AddEvent(ctx, store.Event{ChatID: chat.ID, Kind: store.EvUnpin, Value: int64(r.Unpinned)})
	}
	return r, nil
}

// UnpinEverything unpins all messages including administrator pins.
func (s *Service) UnpinEverything(ctx context.Context, chat *store.Chat) error {
	return s.tg.UnpinAllChatMessages(ctx, chat.ID)
}

// IsMask reports whether msg is sent under a foreign channel identity.
func IsMask(chat *store.Chat, msg *tg.Message) bool {
	sc := msg.SenderChat
	if sc == nil || msg.IsAutomaticForward || sc.ID == chat.ID {
		return false
	}
	return chat.LinkedChatID == 0 || sc.ID != chat.LinkedChatID
}

// OnMask deletes a foreign-channel message and bans that channel identity
// from the chat. It reports whether it acted.
func (s *Service) OnMask(ctx context.Context, chat *store.Chat, msg *tg.Message) bool {
	if !chat.Settings.Channel.BlockMasks || !IsMask(chat, msg) {
		return false
	}
	if err := s.tg.DeleteMessage(ctx, chat.ID, msg.MessageID); err != nil && !tg.IsGone(err) {
		s.log.Warn("delete mask message", "chat", chat.ID, "error", err)
	}
	if err := s.tg.BanChatSenderChat(ctx, chat.ID, msg.SenderChat.ID); err != nil {
		s.log.Warn("ban sender chat", "chat", chat.ID, "sender", msg.SenderChat.ID, "error", err)
		return true
	}
	_ = s.st.AddEvent(ctx, store.Event{ChatID: chat.ID, Kind: store.EvMask, UserID: msg.SenderChat.ID, Data: msg.SenderChat.Title})
	return true
}

// Wait blocks until pending automatic unpins finished.
func (s *Service) Wait() { s.wg.Wait() }
