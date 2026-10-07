package api

import (
	"strings"
	"testing"
)

func TestTelegramSettingsAreCheckedWhenSaved(t *testing.T) {
	c := newTestAPI(t)
	c.do("POST", "/api/v1/auth/setup", map[string]any{"setup_token": testSetupToken, "username": "admin", "password": "password123"}, 200)
	const token = "1234567890:AAEabcdefghijklmnopqrstuvwxyz012345"
	// The whole BotFather message is accepted; only the token is kept.
	saved := c.do("PUT", "/api/v1/settings", map[string]any{"telegram.bot_token": "Use this token to access the HTTP API:\n" + token + "\nKeep your token secure"}, 200)
	if got := saved["telegram.bot_token"].(string); !strings.HasPrefix(got, token[:4]) || !strings.HasSuffix(got, token[len(token)-4:]) || len([]rune(got)) != 12 {
		t.Fatalf("token not normalised: %q", got)
	}
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.bot_token": "@my_bot"}, 400)
	// The bot's own name is the usual mistake for a chat id.
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.chat_id": "@my_alert_bot"}, 400)
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.chat_id": "not a chat"}, 400)
	// So is the bot's own number, which is the first half of its token.
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.chat_id": "1234567890"}, 400)
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.chat_id": "-1001234567890"}, 200)
	c.do("PUT", "/api/v1/settings", map[string]any{"telegram.chat_id": ""}, 200)
}
