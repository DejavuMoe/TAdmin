package userbot

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const (
	replyPollInterval = time.Second
	replyMaxRunes     = 300
)

// RefreshResult is the outcome of RefreshPreview.
type RefreshResult struct {
	Outcome Outcome
	// Reply holds the first 300 runes of the bot reply ("" if none arrived).
	Reply string
	// Duration from sending to receiving the reply (or giving up).
	Duration time.Duration
}

// RefreshPreview sends url to @WebpageBot and waits (Options.ReplyTimeout,
// default 25s) for the bot reply that follows our message. Calls are
// serialized. On timeout it returns Outcome "unknown" together with
// ErrTimeout; on flood control a *FloodWaitError.
func (m *Manager) RefreshPreview(ctx context.Context, url string) (RefreshResult, error) {
	url = strings.TrimSpace(url)
	if url == "" || strings.ContainsAny(url, " \n\t") {
		return RefreshResult{Outcome: OutcomeUnknown}, errors.New("userbot: invalid url")
	}
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	m.mu.Lock()
	until := m.sendFlood
	m.mu.Unlock()
	if wait := time.Until(until); wait > 0 {
		return RefreshResult{Outcome: OutcomeUnknown}, &FloodWaitError{Wait: wait.Round(time.Second)}
	}

	client, st, err := m.waitConnected(ctx)
	if err != nil {
		return RefreshResult{Outcome: OutcomeUnknown}, err
	}
	if st != StateReady {
		return RefreshResult{Outcome: OutcomeUnknown}, ErrNotReady
	}
	api := client.API()
	start := time.Now()

	peer, sentID, err := m.sendToBot(ctx, api, url)
	if err != nil {
		var fw *FloodWaitError
		if errors.As(err, &fw) {
			m.mu.Lock()
			m.sendFlood = time.Now().Add(fw.Wait)
			m.mu.Unlock()
		}
		return RefreshResult{Outcome: OutcomeUnknown, Duration: time.Since(start)}, err
	}

	reply, err := m.awaitReply(ctx, api, peer, sentID)
	res := RefreshResult{Outcome: OutcomeUnknown, Duration: time.Since(start)}
	if err != nil {
		return res, err
	}
	_, _ = api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: peer, MaxID: reply.ID})
	res.Reply = truncateRunes(reply.Message, replyMaxRunes)
	res.Outcome = ClassifyReply(reply.Message)
	m.log.Debug("preview refreshed", "outcome", res.Outcome, "took", res.Duration)
	return res, nil
}

// sendToBot sends text to the preview bot and returns the peer and the id of
// our message. It unblocks the bot and re-resolves a stale peer once.
func (m *Manager) sendToBot(ctx context.Context, api *tg.Client, text string) (*tg.InputPeerUser, int, error) {
	peer, err := m.botPeer(ctx, api)
	if err != nil {
		return nil, 0, err
	}
	id, err := m.send(ctx, api, peer, text)
	switch {
	case err == nil:
	case tgerr.Is(err, "YOU_BLOCKED_USER"):
		m.log.Info("preview bot was blocked, unblocking")
		if _, uerr := api.ContactsUnblock(ctx, &tg.ContactsUnblockRequest{ID: peer}); uerr != nil {
			return nil, 0, m.observe(uerr)
		}
		if _, serr := m.send(ctx, api, peer, "/start"); serr != nil {
			return nil, 0, m.observe(serr)
		}
		id, err = m.send(ctx, api, peer, text)
	case tgerr.Is(err, "PEER_ID_INVALID", "USER_ID_INVALID", "INPUT_USER_DEACTIVATED"):
		m.mu.Lock()
		m.webpagePeer = nil
		m.mu.Unlock()
		if peer, err = m.botPeer(ctx, api); err != nil {
			return nil, 0, err
		}
		id, err = m.send(ctx, api, peer, text)
	}
	if err != nil {
		return nil, 0, m.observe(err)
	}
	if id == 0 {
		// Fall back to looking up our own message.
		if id, err = m.lastOutgoingID(ctx, api, peer, text); err != nil {
			return nil, 0, err
		}
	}
	return peer, id, nil
}

// send sends one message and returns its id (0 if it cannot be determined
// from the response). The error is returned unmapped.
func (m *Manager) send(ctx context.Context, api *tg.Client, peer *tg.InputPeerUser, text string) (int, error) {
	rid, err := randomID()
	if err != nil {
		return 0, err
	}
	upd, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: rid,
	})
	if err != nil {
		return 0, err
	}
	return sentMessageID(upd, rid), nil
}

// botPeer resolves (once) and caches the preview bot peer.
func (m *Manager) botPeer(ctx context.Context, api *tg.Client) (*tg.InputPeerUser, error) {
	m.mu.Lock()
	cached := m.webpagePeer
	m.mu.Unlock()
	if cached != nil {
		p := *cached
		return &p, nil
	}
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: m.opts.BotUsername})
	if err != nil {
		return nil, m.observe(err)
	}
	p, err := peerFromResolved(res)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.webpagePeer = p
	m.mu.Unlock()
	out := *p
	return &out, nil
}

// peerFromResolved extracts the user peer (with access hash).
func peerFromResolved(res *tg.ContactsResolvedPeer) (*tg.InputPeerUser, error) {
	pu, ok := res.Peer.(*tg.PeerUser)
	if !ok {
		return nil, fmt.Errorf("userbot: preview bot resolved to %T", res.Peer)
	}
	for _, u := range res.Users {
		if user, ok := u.AsNotEmpty(); ok && user.ID == pu.UserID {
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
	}
	return nil, errors.New("userbot: preview bot user missing from resolve result")
}

// awaitReply polls the bot chat until an incoming message newer than sentID
// arrives or the reply timeout elapses.
func (m *Manager) awaitReply(ctx context.Context, api *tg.Client, peer *tg.InputPeerUser, sentID int) (*tg.Message, error) {
	rctx, cancel := context.WithTimeout(ctx, m.opts.ReplyTimeout)
	defer cancel()
	ticker := time.NewTicker(replyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rctx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrTimeout
		case <-ticker.C:
		}
		hist, err := api.MessagesGetHistory(rctx, &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: 10,
			MinID: sentID,
		})
		if err != nil {
			if rctx.Err() != nil {
				continue // reported by the select above
			}
			mapped := m.observe(err)
			var fw *FloodWaitError
			if errors.As(mapped, &fw) || errors.Is(mapped, ErrNotReady) {
				return nil, mapped
			}
			m.log.Debug("poll preview bot history", "err", mapped)
			continue
		}
		if msg := firstReply(hist, sentID); msg != nil {
			return msg, nil
		}
	}
}

// firstReply returns the earliest incoming message with id > sentID.
func firstReply(hist tg.MessagesMessagesClass, sentID int) *tg.Message {
	mod, ok := hist.AsModified()
	if !ok {
		return nil
	}
	var best *tg.Message
	for _, mc := range mod.GetMessages() {
		msg, ok := mc.(*tg.Message)
		if !ok || msg.Out || msg.ID <= sentID {
			continue
		}
		if best == nil || msg.ID < best.ID {
			best = msg
		}
	}
	return best
}

// lastOutgoingID finds our most recent outgoing message with the given text.
func (m *Manager) lastOutgoingID(ctx context.Context, api *tg.Client, peer *tg.InputPeerUser, text string) (int, error) {
	hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 10})
	if err != nil {
		return 0, m.observe(err)
	}
	if mod, ok := hist.AsModified(); ok {
		for _, mc := range mod.GetMessages() { // newest first
			if msg, ok := mc.(*tg.Message); ok && msg.Out && msg.Message == text {
				return msg.ID, nil
			}
		}
	}
	return 0, errors.New("userbot: sent message not found")
}

// sentMessageID extracts our message id from a messages.sendMessage result.
func sentMessageID(upd tg.UpdatesClass, randomID int64) int {
	var list []tg.UpdateClass
	switch u := upd.(type) {
	case *tg.UpdateShortSentMessage:
		return u.ID
	case *tg.Updates:
		list = u.Updates
	case *tg.UpdatesCombined:
		list = u.Updates
	default:
		return 0
	}
	for _, uc := range list {
		if v, ok := uc.(*tg.UpdateMessageID); ok && v.RandomID == randomID {
			return v.ID
		}
	}
	for _, uc := range list {
		if v, ok := uc.(*tg.UpdateNewMessage); ok {
			if msg, ok := v.Message.(*tg.Message); ok && msg.Out {
				return msg.ID
			}
		}
	}
	return 0
}

// randomID returns a non-zero random int64 for random_id.
func randomID() (int64, error) {
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if v := int64(binary.LittleEndian.Uint64(b[:])); v != 0 {
			return v, nil
		}
	}
}
