// Package roles answers "who may do what" questions: the configured owner
// and live Telegram administrators, cached briefly per chat.
package roles

import (
	"context"
	"sync"
	"time"

	"tadmin/internal/tg"
)

// AdminSource lists chat administrators (implemented by *tg.Client).
type AdminSource interface {
	GetChatAdministrators(ctx context.Context, chatID int64) ([]tg.ChatMember, error)
}

type entry struct {
	at     time.Time
	admins map[int64]tg.ChatMember
}

// Cache caches administrator lists for a short TTL.
type Cache struct {
	src   AdminSource
	owner int64
	ttl   time.Duration

	mu    sync.Mutex
	chats map[int64]*entry
	// flight coalesces concurrent refreshes of the same chat.
	flight map[int64]chan struct{}
}

// New creates a cache.
func New(src AdminSource, owner int64, ttl time.Duration) *Cache {
	return &Cache{src: src, owner: owner, ttl: ttl, chats: map[int64]*entry{}, flight: map[int64]chan struct{}{}}
}

// Owner returns the configured owner id.
func (c *Cache) Owner() int64 { return c.owner }

// IsOwner reports whether userID is the configured owner.
func (c *Cache) IsOwner(userID int64) bool { return userID == c.owner }

// Invalidate drops the cached list for chatID.
func (c *Cache) Invalidate(chatID int64) {
	c.mu.Lock()
	delete(c.chats, chatID)
	c.mu.Unlock()
}

// Admins returns the administrator map of chatID.
func (c *Cache) Admins(ctx context.Context, chatID int64) (map[int64]tg.ChatMember, error) {
	for {
		c.mu.Lock()
		if e, ok := c.chats[chatID]; ok && time.Since(e.at) < c.ttl {
			c.mu.Unlock()
			return e.admins, nil
		}
		if ch, ok := c.flight[chatID]; ok {
			c.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		ch := make(chan struct{})
		c.flight[chatID] = ch
		c.mu.Unlock()

		list, err := c.src.GetChatAdministrators(ctx, chatID)
		c.mu.Lock()
		delete(c.flight, chatID)
		close(ch)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		m := make(map[int64]tg.ChatMember, len(list))
		for _, a := range list {
			m[a.User.ID] = a
		}
		c.chats[chatID] = &entry{at: time.Now(), admins: m}
		c.mu.Unlock()
		return m, nil
	}
}

// IsAdmin reports whether userID is an administrator of chatID (the owner
// always counts as one).
func (c *Cache) IsAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	if userID == c.owner {
		return true, nil
	}
	m, err := c.Admins(ctx, chatID)
	if err != nil {
		return false, err
	}
	_, ok := m[userID]
	return ok, nil
}

// CanModerate reports whether userID may restrict members in chatID.
func (c *Cache) CanModerate(ctx context.Context, chatID, userID int64) (bool, error) {
	if userID == c.owner {
		return true, nil
	}
	m, err := c.Admins(ctx, chatID)
	if err != nil {
		return false, err
	}
	a, ok := m[userID]
	return ok && a.CanRestrict(), nil
}
