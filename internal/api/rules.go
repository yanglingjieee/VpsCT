package api

import (
	"net/http"
	"strings"

	"ctlvps/internal/domain"
	"ctlvps/internal/httpx"
	"ctlvps/internal/ruleset"
)

// RulesView is the panel's rules as the editor shows them: what is in effect
// (the built-in minimum while nothing was saved) and the lists a rule may
// name.
type RulesView struct {
	domain.Rules
	Saved bool           `json:"saved"`
	Lists []ruleset.List `json:"lists"`
}

func (a *API) rulesView(r *http.Request) RulesView {
	saved, _ := a.Store.Rules(r.Context())
	v := RulesView{Rules: saved, Saved: !saved.UpdatedAt.IsZero(), Lists: ruleset.Lists}
	v.Rules.Rules, v.GroupName = a.Subs.Rules(r.Context())
	return v
}

func (a *API) getRules(w http.ResponseWriter, r *http.Request) error {
	httpx.OK(w, a.rulesView(r))
	return nil
}

// putRules saves the rules after reading them once: a line that cannot be
// read would break every user's next refresh.
func (a *API) putRules(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Rules     string `json:"rules"`
		GroupName string `json:"group_name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	v := domain.Rules{Rules: strings.ReplaceAll(in.Rules, "\r\n", "\n"), GroupName: strings.TrimSpace(in.GroupName)}
	if v.GroupName == "" {
		v.GroupName = ruleset.DefaultGroup
	}
	if len(v.Rules) > 1<<20 {
		return httpx.BadRequest("规则超过 1 MB")
	}
	if len(v.GroupName) > 64 || strings.ContainsAny(v.GroupName, ",\r\n\t\"'#") {
		return httpx.BadRequest("线路组的名字不能超过 64 个字符，也不能有逗号、引号和 #")
	}
	switch strings.ToUpper(v.GroupName) {
	case ruleset.ActionProxy, ruleset.ActionDirect, ruleset.ActionReject:
		return httpx.BadRequest("线路组不能叫 PROXY、DIRECT 或 REJECT")
	}
	if _, err := ruleset.Parse(v.Rules); err != nil {
		return httpx.BadRequest(err.Error())
	}
	if err := a.Store.SaveRules(r.Context(), &v); err != nil {
		return err
	}
	a.audit(r, "rules.update", "", nil)
	httpx.OK(w, a.rulesView(r))
	return nil
}
