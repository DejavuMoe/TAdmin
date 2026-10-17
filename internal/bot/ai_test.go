package bot

import (
	"strings"
	"testing"

	"tadmin/internal/i18n"
	"tadmin/internal/store"
)

func TestLayaPanelHintOnly(t *testing.T) {
	h := newHarness(t)
	h.b.cfg.TypeSafeProvider = "laya"
	h.b.AIReady = true
	if err := h.st.UpsertChat(h.ctx, &store.Chat{ID: groupID, Title: "Test", Status: store.ChatActive}); err != nil {
		t.Fatal(err)
	}
	c, err := h.st.UpdateSettings(h.ctx, groupID, func(s *store.Settings) { s.Verify.AI = "review" })
	if err != nil {
		t.Fatal(err)
	}
	s := h.b.securityScreen(h.ctx, c, "zh-CN")
	if !strings.Contains(s.text, i18n.T("zh-CN", "ai_desc_hint")) {
		t.Fatal("panel does not show effective hint mode")
	}
	if c.Settings.Verify.AI != "review" {
		t.Fatal("render changed persisted preference")
	}
	for _, want := range []string{"off", "hint", "off"} {
		h.callback(ownerID, ownerID, 1, "s:"+itoa(groupID)+":ai")
		c, err = h.st.GetChat(h.ctx, groupID)
		if err != nil || c.Settings.Verify.AI != want {
			t.Fatalf("want %s, got %+v / %v", want, c, err)
		}
	}
}
