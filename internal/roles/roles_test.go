package roles

import (
	"context"
	"testing"
	"time"

	"tadmin/internal/tg"
)

type adminSourceFunc func(context.Context, int64) ([]tg.ChatMember, error)

func (f adminSourceFunc) GetChatAdministrators(ctx context.Context, chat int64) ([]tg.ChatMember, error) {
	return f(ctx, chat)
}

func TestInvalidationDuringRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	src := adminSourceFunc(func(context.Context, int64) ([]tg.ChatMember, error) {
		calls++
		if calls == 1 {
			close(started)
			<-release
			return []tg.ChatMember{{User: tg.User{ID: 42}, Status: tg.StatusAdministrator, CanRestrictMembers: true}}, nil
		}
		return nil, nil
	})
	c := New(src, 1, time.Minute)
	result := make(chan bool, 1)
	go func() { ok, _ := c.CanModerate(context.Background(), -1, 42); result <- ok }()
	<-started
	c.Invalidate(-1)
	close(release)
	if <-result {
		t.Fatal("invalidated in-flight result restored revoked authority")
	}
}
