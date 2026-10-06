package provision

import (
	"encoding/json"
	"errors"

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
