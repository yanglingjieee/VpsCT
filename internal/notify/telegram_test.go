package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenAndChatAreUnderstoodAsPasted(t *testing.T) {
	const token = "1234567890:AAEabcdefghijklmnopqrstuvwxyz012345"
	for _, raw := range []string{token, "  " + token + "\n", "bot" + token, "Use this token to access the HTTP API:\n" + token + "\nKeep your token secure"} {
		if got, ok := NormalizeToken(raw); !ok || got != token {
			t.Fatalf("token not found in %q: %q", raw, got)
		}
	}
	if _, ok := NormalizeToken("@my_bot"); ok {
		t.Fatal("a bot name is not a token")
	}
	for chat, ok := range map[string]bool{"123456": true, "-1001234567890": true, "@my_channel": true, "@tudou_bot": false, "t.me/x": false, "": true} {
		if (CheckChat(chat) == "") != ok {
			t.Fatalf("chat %q: %q", chat, CheckChat(chat))
		}
	}
}

func TestFailuresSayWhatToDo(t *testing.T) {
	const token = "1234567890:AAEabcdefghijklmnopqrstuvwxyz012345"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case !strings.HasPrefix(r.URL.Path, "/bot"+token+"/"):
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":404,"description":"Not Found"}`))
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"tudou_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"message":{"chat":{"id":42,"type":"private","first_name":"Y"}}},{"my_chat_member":{"chat":{"id":-100,"type":"supergroup","title":"家"}}},{"message":{"chat":{"id":42,"type":"private","first_name":"Y"}}}]}`))
		default:
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["chat_id"] == "42" {
				_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
				return
			}
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
		}
	}))
	defer srv.Close()
	cfg := [2]string{token, "42"}
	tg := New(func(context.Context) (string, string) { return cfg[0], cfg[1] }, nil)
	tg.Base = srv.URL
	ctx := context.Background()
	if err := tg.Test(ctx, "site"); err != nil {
		t.Fatal(err)
	}
	if bot, err := tg.Bot(ctx); err != nil || bot != "@tudou_bot" {
		t.Fatalf("bot: %q %v", bot, err)
	}
	chats, err := tg.Chats(ctx)
	if err != nil || len(chats) != 2 || chats[0].ID != "42" || chats[1].Title != "家" {
		t.Fatalf("chats, newest first and once each: %+v %v", chats, err)
	}
	cfg[1] = "@tudou_bot"
	if err := tg.Test(ctx, "site"); err == nil || !strings.Contains(err.Error(), "Chat ID") {
		t.Fatalf("wrong chat must be explained: %v", err)
	}
	cfg[0] = "junk"
	if err := tg.Test(ctx, "site"); err == nil || !strings.Contains(err.Error(), "Bot Token") || strings.Contains(err.Error(), "junk") {
		t.Fatalf("wrong token must be explained without echoing it: %v", err)
	}
	cfg[0] = ""
	if err := tg.Test(ctx, "site"); err != ErrNotConfigured {
		t.Fatalf("unconfigured: %v", err)
	}
}
