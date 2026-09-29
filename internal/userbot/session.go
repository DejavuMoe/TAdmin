package userbot

import (
	"context"
	"sync/atomic"

	"github.com/gotd/td/session"
)

// SessionStore persists the opaque MTProto session blob. The data is highly
// sensitive (it grants full account access) and must be stored with care.
type SessionStore interface {
	// LoadSession returns the stored session, or (nil, nil) when there is none.
	LoadSession(ctx context.Context) ([]byte, error)
	// StoreSession replaces the stored session. Empty data clears it.
	StoreSession(ctx context.Context, data []byte) error
}

// sessionAdapter adapts SessionStore to gotd's session.Storage. Each gotd
// client gets its own adapter so that a client being torn down after logout
// cannot resurrect a cleared session: once disabled, writes are dropped.
type sessionAdapter struct {
	store    SessionStore
	loaded   atomic.Bool // a non-empty session was loaded
	disabled atomic.Bool
}

var _ session.Storage = (*sessionAdapter)(nil)

// LoadSession implements session.Storage.
func (a *sessionAdapter) LoadSession(ctx context.Context) ([]byte, error) {
	if a.store == nil || a.disabled.Load() {
		return nil, session.ErrNotFound
	}
	data, err := a.store.LoadSession(ctx)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, session.ErrNotFound
	}
	a.loaded.Store(true)
	return data, nil
}

// StoreSession implements session.Storage.
func (a *sessionAdapter) StoreSession(ctx context.Context, data []byte) error {
	if a.store == nil || a.disabled.Load() {
		return nil
	}
	return a.store.StoreSession(ctx, data)
}
