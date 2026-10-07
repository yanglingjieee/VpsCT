// Package report tells the operator what happens on the network. It follows
// each condition from the moment it appears until it clears and says both
// once, with what the operator needs to know to act on it, and it writes the
// daily report. What it has said is kept in the store, so a restart neither
// repeats an alert nor loses the all-clear.
package report

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/notify"
	"ctlvps/internal/store"
	"ctlvps/internal/traffic"
)

// Reporter watches the servers, users and feeds and posts to Telegram. Its
// methods are safe on a nil Reporter, which says nothing.
type Reporter struct {
	Store    *store.Store
	Traffic  *traffic.Ingestor
	Telegram *notify.Telegram
	Logger   *slog.Logger

	started time.Time

	mu       sync.Mutex
	open     map[string]domain.Incident // what has not cleared, by key
	restarts map[string]int             // automatic restarts already told, by core
}

// New builds a Reporter that knows what was open when the panel last ran.
func New(ctx context.Context, st *store.Store, tr *traffic.Ingestor, tg *notify.Telegram, logger *slog.Logger) (*Reporter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Reporter{Store: st, Traffic: tr, Telegram: tg, Logger: logger, started: st.Now(), open: map[string]domain.Incident{}, restarts: map[string]int{}}
	list, err := st.OpenIncidents(ctx)
	if err != nil {
		return nil, err
	}
	for _, inc := range list {
		r.open[inc.Key] = inc
	}
	return r, nil
}

// notice is one message: a headline and the lines under it.
type notice struct {
	title string // plain text
	body  []string
	// since is when the condition began, when that is earlier than now.
	since time.Time
}

func (n notice) html() string {
	return strings.Join(append([]string{"<b>" + esc(n.title) + "</b>"}, n.body...), "\n")
}

// key names a condition: its kind, what it is about, and which one.
func key(kind, subject string, id int64, extra ...string) string {
	k := kind + "/" + subject + "/" + strconv.FormatInt(id, 10)
	for _, e := range extra {
		k += "/" + e
	}
	return k
}

// about splits a key back into its kind, subject and id.
func about(k string) (kind, subject string, id int64) {
	parts := strings.SplitN(k, "/", 4)
	if len(parts) < 3 {
		return "", "", 0
	}
	id, _ = strconv.ParseInt(parts[2], 10, 64)
	return parts[0], parts[1], id
}

func (r *Reporter) isOpen(k string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.open[k]
	return ok
}

// openUnder lists the open keys that start with prefix.
func (r *Reporter) openUnder(prefix string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for k := range r.open {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// begin records that k started; false when that is already known.
func (r *Reporter) begin(ctx context.Context, k, title string, since time.Time) (domain.Incident, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if inc, ok := r.open[k]; ok {
		return inc, false
	}
	if since.IsZero() {
		since = r.Store.Now()
	}
	inc := domain.Incident{Key: k, Title: title, OpenedAt: since}
	if err := r.Store.OpenIncident(ctx, &inc); err != nil {
		r.Logger.Warn("incident not recorded", "key", k, "err", err)
		return inc, false
	}
	r.open[k] = inc
	return inc, true
}

// end records that k cleared; false when it was not open.
func (r *Reporter) end(ctx context.Context, k string) (domain.Incident, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inc, ok := r.open[k]
	if !ok {
		return inc, false
	}
	now := r.Store.Now()
	if err := r.Store.ResolveIncident(ctx, inc.ID, now); err != nil {
		r.Logger.Warn("incident not resolved", "key", k, "err", err)
		return inc, false
	}
	delete(r.open, k)
	inc.ResolvedAt = &now
	return inc, true
}

// attach remembers which message announced an incident.
func (r *Reporter) attach(ctx context.Context, inc domain.Incident, message int64) {
	if message == 0 {
		return
	}
	if err := r.Store.SetIncidentMessage(ctx, inc.ID, message); err != nil {
		r.Logger.Warn("incident message not recorded", "key", inc.Key, "err", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.open[inc.Key]; ok && cur.ID == inc.ID {
		cur.MessageID = message
		r.open[inc.Key] = cur
	}
}

// post sends a notice and returns its message id, 0 when it did not go out.
// A request that ends early must not cost the message it caused.
func (r *Reporter) post(ctx context.Context, n notice, replyTo int64) int64 {
	if r.Telegram == nil {
		return 0
	}
	id, err := r.Telegram.Post(context.WithoutCancel(ctx), n.html(), replyTo)
	if err != nil && !errors.Is(err, notify.ErrNotConfigured) {
		r.Logger.Warn("telegram send failed", "title", n.title, "err", err)
	}
	return id
}

// track brings what was said about k in line with whether it holds now. It
// announces the start once and, when cleared is given, answers that message
// once it is over; with cleared nil the end passes without a word. The
// notices are only written when something changed.
func (r *Reporter) track(ctx context.Context, k string, on bool, started func() notice, cleared func(domain.Incident) notice) {
	if on {
		if r.isOpen(k) {
			return
		}
		n := started()
		if inc, ok := r.begin(ctx, k, n.title, n.since); ok {
			r.attach(ctx, inc, r.post(ctx, n, 0))
		}
		return
	}
	if inc, ok := r.end(ctx, k); ok && cleared != nil {
		r.post(ctx, cleared(inc), inc.MessageID)
	}
}

// once tells something that is over as soon as it is told.
func (r *Reporter) once(ctx context.Context, k string, n notice) {
	now := r.Store.Now()
	inc := domain.Incident{Key: k, Title: n.title, OpenedAt: now, ResolvedAt: &now, MessageID: r.post(ctx, n, 0)}
	if err := r.Store.OpenIncident(ctx, &inc); err != nil {
		r.Logger.Warn("incident not recorded", "key", k, "err", err)
	}
}

// sweep closes what is still open about servers, users and feeds that are
// gone, so that nothing waits for an all-clear that cannot come.
func (r *Reporter) sweep(ctx context.Context, sc *scene) {
	r.mu.Lock()
	keys := make([]string, 0, len(r.open))
	for k := range r.open {
		keys = append(keys, k)
	}
	r.mu.Unlock()
	if len(keys) == 0 {
		return
	}
	users := map[int64]bool{}
	if shares, err := r.Store.ListShares(ctx, nil); err == nil {
		for _, sh := range shares {
			users[sh.ID] = sh.Status != domain.ShareRevoked
		}
	} else {
		return
	}
	feeds := map[int64]bool{}
	if exts, err := r.Store.ListExternal(ctx); err == nil {
		for _, e := range exts {
			feeds[e.ID] = true
		}
	} else {
		return
	}
	for _, k := range keys {
		_, subject, id := about(k)
		gone := false
		switch subject {
		case "server":
			_, ok := sc.server[id]
			gone = !ok
		case "user":
			gone = !users[id]
		case "external":
			gone = !feeds[id]
		}
		if gone {
			r.end(ctx, k)
		}
	}
}
