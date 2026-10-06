package api

import (
	"net/http"
	"strconv"
	"strings"

	"ctlvps/internal/domain"
	"ctlvps/internal/httpx"
	"ctlvps/internal/subscription"
)

// RulesetView adds what the list shows without loading every profile.
type RulesetView struct {
	domain.Ruleset
	Formats []string `json:"formats"` // client families it was written for
	Users   int      `json:"users"`
	Default bool     `json:"default"`
}

func (a *API) rulesetViews(r *http.Request, list []domain.Ruleset) []RulesetView {
	shares, _ := a.Store.ListShares(r.Context(), nil)
	def := int64(a.Store.GetSettingInt(r.Context(), domain.SettingDefaultRuleset, 0))
	out := make([]RulesetView, 0, len(list))
	for _, rs := range list {
		v := RulesetView{Ruleset: rs, Formats: []string{}, Default: rs.ID == def}
		for _, k := range subscription.TemplateKinds {
			if strings.TrimSpace(rs.Content(k)) != "" {
				v.Formats = append(v.Formats, k)
			}
		}
		for _, sh := range shares {
			if sh.RulesetID != nil && *sh.RulesetID == rs.ID && sh.Status != domain.ShareRevoked {
				v.Users++
			}
		}
		out = append(out, v)
	}
	return out
}

// listRulesets also reports the built-in "no rules" profile, which is not a
// row: who uses it and whether it is the default for new users.
func (a *API) listRulesets(w http.ResponseWriter, r *http.Request) error {
	list, err := a.Store.ListRulesets(r.Context())
	if err != nil {
		return err
	}
	shares, _ := a.Store.ListShares(r.Context(), nil)
	plain := 0
	for _, sh := range shares {
		if sh.RulesetID == nil && sh.Status != domain.ShareRevoked {
			plain++
		}
	}
	builtin := map[string]string{}
	for _, k := range subscription.TemplateKinds {
		builtin[k] = subscription.NoRules(k)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"list":       a.rulesetViews(r, list),
		"none_users": plain,
		"default_id": a.Store.GetSettingInt(r.Context(), domain.SettingDefaultRuleset, 0),
		"none":       builtin,
	})
	return nil
}

type rulesetInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Mihomo       string `json:"mihomo"`
	Shadowrocket string `json:"shadowrocket"`
	Surge        string `json:"surge"`
	SingBox      string `json:"singbox"`
	SortOrder    int    `json:"sort_order"`
}

// apply stores the profiles after rendering each once: a profile that does
// not parse would break every user's next refresh.
func (in rulesetInput) apply(rs *domain.Ruleset) error {
	rs.Name, rs.Description, rs.SortOrder = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), in.SortOrder
	rs.Mihomo, rs.Shadowrocket, rs.Surge, rs.SingBox = in.Mihomo, in.Shadowrocket, in.Surge, in.SingBox
	labels := map[string]string{"mihomo": "Clash / Mihomo", "shadowrocket": "Shadowrocket", "surge": "Surge", "singbox": "sing-box"}
	for _, k := range subscription.TemplateKinds {
		content := rs.Content(k)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if len(content) > 1<<20 {
			return httpx.BadRequest(labels[k] + " 的规则超过 1 MB")
		}
		b := &subscription.Bundle{Name: "check", Template: &domain.RuleTemplate{Kind: k, Content: content}}
		if _, err := subscription.RenderBundle(b, k); err != nil {
			return httpx.BadRequest(labels[k] + "：" + err.Error())
		}
	}
	return nil
}

func (a *API) createRuleset(w http.ResponseWriter, r *http.Request) error {
	var in rulesetInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var rs domain.Ruleset
	if err := in.apply(&rs); err != nil {
		return err
	}
	if err := a.Store.CreateRuleset(r.Context(), &rs); err != nil {
		return httpx.BadRequest(err.Error())
	}
	a.audit(r, "ruleset.create", rs.Name, nil)
	httpx.JSON(w, http.StatusCreated, a.rulesetViews(r, []domain.Ruleset{rs})[0])
	return nil
}

func (a *API) updateRuleset(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	rs, err := a.Store.GetRuleset(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	var in rulesetInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := in.apply(&rs); err != nil {
		return err
	}
	if err := a.Store.UpdateRuleset(r.Context(), &rs); err != nil {
		return httpx.BadRequest(err.Error())
	}
	a.audit(r, "ruleset.update", rs.Name, nil)
	httpx.OK(w, a.rulesetViews(r, []domain.Ruleset{rs})[0])
	return nil
}

func (a *API) deleteRuleset(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	rs, err := a.Store.GetRuleset(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	if err := a.Store.DeleteRuleset(r.Context(), id); err != nil {
		return err
	}
	if a.Store.GetSettingInt(r.Context(), domain.SettingDefaultRuleset, 0) == int(id) {
		_ = a.Store.SetSettings(r.Context(), map[string]string{domain.SettingDefaultRuleset: "0"})
	}
	a.audit(r, "ruleset.delete", rs.Name, nil)
	httpx.NoContent(w)
	return nil
}

// setDefaultRuleset chooses what new users get (id 0 = no rules).
func (a *API) setDefaultRuleset(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathInt64(r, "id")
	if err != nil {
		return err
	}
	if id != 0 {
		if _, err := a.Store.GetRuleset(r.Context(), id); err != nil {
			return httpx.ErrNotFound
		}
	}
	if err := a.Store.SetSettings(r.Context(), map[string]string{domain.SettingDefaultRuleset: strconv.FormatInt(id, 10)}); err != nil {
		return err
	}
	a.audit(r, "ruleset.default", strconv.FormatInt(id, 10), nil)
	httpx.NoContent(w)
	return nil
}
