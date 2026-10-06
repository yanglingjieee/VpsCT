package provision

import (
	"encoding/json"
	"errors"
	"strings"

	"ctlvps/internal/domain"
)

// NewMember builds one more credential on parent's listener: the same address
// and handshake, its own UUID. The caller assigns the share and persists it.
func NewMember(parent domain.Node, name string) (domain.Node, error) {
	if parent.ID == 0 || parent.ServerID == nil || parent.Protocol != domain.ProtocolVLESS || parent.AttachNodeID != nil {
		return domain.Node{}, errors.New("只有自己部署的 VLESS 节点可以多人共用")
	}
	id := parent.ID
	m := domain.Node{Name: name, Protocol: parent.Protocol, Source: domain.NodeDeployed, ServerID: parent.ServerID,
		Core: parent.Core, AttachNodeID: &id, Enabled: true, Tags: []string{}}
	if err := SyncMember(&m, parent, true); err != nil {
		return domain.Node{}, err
	}
	return m, nil
}

// SyncMember copies the listener's current connection parameters into m. The
// member keeps its UUID unless rotate is set or it has none yet. It reports
// through the node itself; callers compare before saving.
func SyncMember(m *domain.Node, parent domain.Node, rotate bool) error {
	client := map[string]any{}
	if err := json.Unmarshal(parent.Params, &client); err != nil {
		return err
	}
	parentSrv := map[string]any{}
	_ = json.Unmarshal(parent.ServerParams, &parentSrv)
	own := map[string]any{}
	_ = json.Unmarshal(m.ServerParams, &own)
	uuid, _ := own["uuid"].(string)
	if rotate || len(uuid) != 36 {
		uuid = UUID()
	}
	flow, _ := parentSrv["flow"].(string)
	if flow == "" {
		flow = "xtls-rprx-vision"
	}
	client["uuid"] = uuid
	params, err := json.Marshal(client)
	if err != nil {
		return err
	}
	srv, err := json.Marshal(map[string]any{"uuid": uuid, "flow": flow})
	if err != nil {
		return err
	}
	m.Params, m.ServerParams = params, srv
	m.Server, m.Port, m.ListenPort, m.Core = parent.Server, parent.Port, 0, parent.Core
	return nil
}

// SetSNI changes a VLESS node's Reality handshake target. Keys and user
// credentials stay, so clients only need a refreshed subscription.
func SetSNI(n *domain.Node, sni string) error {
	if n.Protocol != domain.ProtocolVLESS || n.AttachNodeID != nil {
		return errors.New("只有 VLESS 节点可以修改伪装域名")
	}
	if !validHost(sni) {
		return errors.New("伪装域名格式不正确")
	}
	client, srv := map[string]any{}, map[string]any{}
	if err := json.Unmarshal(n.Params, &client); err != nil {
		return err
	}
	if err := json.Unmarshal(n.ServerParams, &srv); err != nil {
		return err
	}
	client["servername"], srv["handshake_server"] = sni, sni
	var err error
	if n.Params, err = json.Marshal(client); err != nil {
		return err
	}
	n.ServerParams, err = json.Marshal(srv)
	return err
}

// SNI returns the Reality handshake target of a VLESS node, if any.
func SNI(n domain.Node) string {
	client := map[string]any{}
	_ = json.Unmarshal(n.Params, &client)
	s, _ := client["servername"].(string)
	return s
}

func validHost(h string) bool {
	if len(h) < 4 || len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
