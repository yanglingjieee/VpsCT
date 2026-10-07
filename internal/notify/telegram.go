// Package notify sends operator alerts (Telegram).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Telegram posts messages through the Bot API.
type Telegram struct {
	Client *http.Client
	// Config returns (token, chatID); called per message so settings edits
	// take effect immediately.
	Config func(ctx context.Context) (string, string)
	Logger *slog.Logger
	// Base is the Bot API origin; tests replace it.
	Base string

	mu       sync.Mutex
	lastSent map[string]time.Time
}

// New builds a Telegram notifier.
func New(cfg func(ctx context.Context) (string, string), logger *slog.Logger) *Telegram {
	return &Telegram{Client: &http.Client{Timeout: 15 * time.Second}, Config: cfg, Logger: logger, Base: "https://api.telegram.org", lastSent: map[string]time.Time{}}
}

var tokenRe = regexp.MustCompile(`\d{6,12}:[A-Za-z0-9_-]{30,64}`)

// NormalizeToken finds the bot token in whatever was pasted: BotFather hands
// it out inside a few lines of text, and people paste all of it.
func NormalizeToken(raw string) (string, bool) {
	t := tokenRe.FindString(raw)
	return t, t != ""
}

// CheckChat explains what is wrong with a chat id for the bot behind token,
// or returns "". A chat id is a number (negative for groups) or the @name of
// a public channel; the bot's own @name or number is the usual mistake.
func CheckChat(raw, token string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if id, _, ok := strings.Cut(token, ":"); ok && raw == id {
		return selfChat
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return ""
	}
	if strings.HasPrefix(raw, "@") && len(raw) > 5 {
		if strings.HasSuffix(strings.ToLower(raw), "bot") {
			return selfChat
		}
		return ""
	}
	return "Chat ID 应该是一串数字（群组是负数），或公开频道的 @名称"
}

const selfChat = "Chat ID 填成了机器人自己。它应该是你的数字 ID：先在 Telegram 里给机器人发一条消息，再点「自动获取」"

// ChatProblem explains why the saved chat cannot receive this bot's messages,
// or returns "". Telegram itself only says so when a message is sent, so a
// chat saved before the check existed would otherwise look fine.
func (t *Telegram) ChatProblem(ctx context.Context, bot string) string {
	token, chat := t.Config(ctx)
	if chat = strings.TrimSpace(chat); bot != "" && strings.EqualFold(chat, bot) {
		return selfChat
	}
	return CheckChat(chat, token)
}

// ErrNotConfigured means no token or chat has been saved yet.
var ErrNotConfigured = errors.New("telegram not configured")

// call runs one Bot API method and decodes its result. Failures come back in
// words the operator can act on instead of a bare status code.
func (t *Telegram) call(ctx context.Context, token, method string, payload any, result any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Base+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return errors.New("连不上 Telegram：" + trimSecret(err.Error(), token))
	}
	defer resp.Body.Close()
	var reply struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&reply)
	if resp.StatusCode == http.StatusOK && reply.OK {
		if result != nil {
			return json.Unmarshal(reply.Result, result)
		}
		return nil
	}
	d := strings.ToLower(reply.Description)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound:
		return errors.New("Bot Token 不对：到 @BotFather 重新复制（形如 123456789:AA…）")
	case strings.Contains(d, "chat not found"):
		return errors.New("找不到这个 Chat ID：先在 Telegram 里给机器人发一条消息，再点「自动获取」")
	case strings.Contains(d, "can't send messages to"):
		// "bot can't send messages to bots" / "the bot can't send messages to the bot"
		return errors.New(selfChat)
	case resp.StatusCode == http.StatusConflict:
		return errors.New("这个机器人正被别的程序收消息（webhook 或另一个程序），自动获取不了：Chat ID 请手动填数字")
	case strings.Contains(d, "blocked by the user") || strings.Contains(d, "can't initiate conversation"):
		return errors.New("机器人还不能给你发消息：在 Telegram 里打开机器人，点 Start")
	case strings.Contains(d, "not enough rights") || strings.Contains(d, "kicked") || strings.Contains(d, "not a member"):
		return errors.New("机器人不在这个群组或频道里，或者没有发言权限")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("发得太频繁，Telegram 暂时限流了，稍后再试")
	case reply.Description != "":
		return fmt.Errorf("Telegram 拒绝了请求：%s", reply.Description)
	}
	return fmt.Errorf("Telegram 返回 HTTP %d", resp.StatusCode)
}

func trimSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "<token>")
}

// Send posts text (Markdown disabled for safety).
func (t *Telegram) Send(ctx context.Context, text string) error {
	token, chat := t.Config(ctx)
	if token == "" || chat == "" {
		return ErrNotConfigured
	}
	return t.call(ctx, token, "sendMessage", map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true}, nil)
}

// SendDedup sends at most once per key within window.
func (t *Telegram) SendDedup(ctx context.Context, key string, window time.Duration, text string) {
	t.mu.Lock()
	if last, ok := t.lastSent[key]; ok && time.Since(last) < window {
		t.mu.Unlock()
		return
	}
	t.lastSent[key] = time.Now()
	t.mu.Unlock()
	if err := t.Send(ctx, text); err != nil && t.Logger != nil && !errors.Is(err, ErrNotConfigured) {
		t.Logger.Warn("telegram send failed", "err", err)
	}
}

// Test sends a probe message.
func (t *Telegram) Test(ctx context.Context, site string) error {
	return t.Send(ctx, "✅ "+site+"：通知测试成功")
}

// Bot returns the @username the saved token belongs to.
func (t *Telegram) Bot(ctx context.Context) (string, error) {
	token, _ := t.Config(ctx)
	if token == "" {
		return "", ErrNotConfigured
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := t.call(ctx, token, "getMe", map[string]any{}, &me); err != nil {
		return "", err
	}
	return "@" + me.Username, nil
}

// Chat is somewhere the bot can be told to post.
type Chat struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"` // private | group | supergroup | channel
}

// Chats lists who has recently written to the bot, newest first: the chat an
// operator just opened is the one they want alerts in.
func (t *Telegram) Chats(ctx context.Context) ([]Chat, error) {
	token, _ := t.Config(ctx)
	if token == "" {
		return nil, ErrNotConfigured
	}
	type chat struct {
		ID        int64  `json:"id"`
		Type      string `json:"type"`
		Title     string `json:"title"`
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	}
	type message struct {
		Chat chat `json:"chat"`
	}
	var updates []struct {
		Message      *message `json:"message"`
		ChannelPost  *message `json:"channel_post"`
		MyChatMember *message `json:"my_chat_member"`
	}
	if err := t.call(ctx, token, "getUpdates", map[string]any{"limit": 100, "timeout": 0, "allowed_updates": []string{"message", "channel_post", "my_chat_member"}}, &updates); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	out := []Chat{}
	for i := len(updates) - 1; i >= 0; i-- {
		for _, m := range []*message{updates[i].Message, updates[i].ChannelPost, updates[i].MyChatMember} {
			if m == nil || m.Chat.ID == 0 || seen[m.Chat.ID] {
				continue
			}
			seen[m.Chat.ID] = true
			title := strings.TrimSpace(m.Chat.Title)
			if title == "" {
				title = strings.TrimSpace(m.Chat.FirstName + " " + m.Chat.LastName)
			}
			if title == "" && m.Chat.Username != "" {
				title = "@" + m.Chat.Username
			}
			out = append(out, Chat{ID: strconv.FormatInt(m.Chat.ID, 10), Title: title, Type: m.Chat.Type})
		}
	}
	return out, nil
}
