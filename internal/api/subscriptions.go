package api

import (
	"net/http"
	"time"

	"ctlvps/internal/domain"
	"ctlvps/internal/httpx"
	"ctlvps/internal/subscription"
	"ctlvps/internal/traffic"
)

// SubscriptionView adds ready-to-copy links.
type SubscriptionView struct {
	domain.Subscription
	Links     map[string]string `json:"links"`
	ShortLink string            `json:"short_link,omitempty"`
	NodeCount int               `json:"node_count"`
	Supported bool              `json:"supported"`
	ShareName string            `json:"share_name,omitempty"`
	NextReset *time.Time        `json:"next_reset,omitempty"`
}

func (a *API) subView(r *http.Request, s domain.Subscription, full bool) SubscriptionView {
	v := SubscriptionView{Subscription: s, Links: map[string]string{}, Supported: s.Kind.Supported()}
	if !v.Supported {
		v.Enabled = false
		v.Token, v.TokenHint, v.ShortCode = "", "", ""
		return v
	}
	base := a.baseURL(r)
	if s.Token != "" {
		for _, f := range subscription.KnownFormats {
			v.Links[f] = base + "/s/" + s.Token + "/" + f
		}
		v.Links["auto"] = base + "/s/" + s.Token
	}
	if s.ShortCode != "" {
		v.ShortLink = base + "/r/" + s.ShortCode
	}
	if !full {
		v.Token = ""
	}
	if s.ShareID != nil {
		if sh, err := a.Store.GetShare(r.Context(), *s.ShareID); err == nil {
			v.ShareName = sh.Name
		}
	}
	if b, err := a.Subs.Build(r.Context(), s); err == nil {
		v.NodeCount = len(b.Proxies)
	}
	if s.ResetDay > 0 {
		nr := traffic.NextReset(a.Store.Now().In(a.Store.Location()), s.ResetDay)
		v.NextReset = &nr
	}
	return v
}

func (a *API) canSeeSubscription(u *domain.User, s domain.Subscription) bool {
	if isAdmin(u) {
		return true
	}
	if s.OwnerUserID == u.ID {
		return true
	}
	for _, id := range s.AllowedUserIDs {
		if id == u.ID {
			return true
		}
	}
	return false
}

func (a *API) renderSubscription(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	s, err := a.Store.GetSubscription(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	if !a.canSeeSubscription(userFrom(r.Context()), s) {
		return httpx.ErrForbidden
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = s.DefaultFormat
	}
	rendered, bundle, err := a.Subs.Render(r.Context(), s, format, a.baseURL(r))
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	httpx.OK(w, map[string]any{"format": rendered.Format, "content_type": rendered.ContentType, "body": string(rendered.Body), "node_count": len(bundle.Proxies)})
	return nil
}

func (a *API) subscriptionAccessLog(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	list, err := a.Store.ListAccessLog(r.Context(), &id, httpx.QueryInt(r, "limit", 100), httpx.QueryInt(r, "offset", 0))
	if err != nil {
		return err
	}
	httpx.OK(w, list)
	return nil
}
