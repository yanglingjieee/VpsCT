package agentproto

import (
	"crypto/sha256"
	"ctlvps/internal/agentbudget"
	"ctlvps/internal/corecompat"
	"ctlvps/internal/mieruconfig"
	"ctlvps/internal/wgconfig"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// ValidateDesired is repeated at the final privileged execution boundary.
func ValidateDesired(d *DesiredState, serverID, lastRevision int64, lastHash string) error {
	if d == nil || d.ServerID != serverID || d.Revision < 1 || d.Revision < lastRevision || (d.Revision == lastRevision && lastHash != "" && d.Hash != lastHash) {
		return errors.New("配置身份或代次无效")
	}
	if len(d.Nodes) > 2048 || len(d.Hash) != 64 || d.Hash != ContentHash(d) {
		return errors.New("配置大小或摘要无效")
	}
	if d.MitaVersion != 0 && d.MitaVersion != 1 {
		return errors.New("mita 能力版本无效")
	}
	if d.NetworkForwardVersion != 0 && (d.NetworkForwardVersion != NetworkForwardVersion || d.NetworkBindingVersion != NetworkBindingVersion) {
		return errors.New("不支持的固定转发版本或缺少网络绑定声明")
	}
	if len(d.Forwards) > agentbudget.ActiveForwards || (len(d.Forwards) > 0 && d.NetworkForwardVersion != NetworkForwardVersion) {
		return errors.New("固定转发数量或版本声明无效")
	}
	if d.NetworkBindingVersion != 0 && d.NetworkBindingVersion != NetworkBindingVersion {
		return errors.New("不支持的网络绑定配置版本")
	}
	if d.NetworkEgressVersion != 0 && (d.NetworkEgressVersion != NetworkEgressVersion || d.NetworkBindingVersion != NetworkBindingVersion) {
		return errors.New("不支持的中转出口配置版本或缺少网络绑定声明")
	}
	if d.NetworkWireGuardVersion != 0 && (d.NetworkWireGuardVersion != NetworkWireGuardVersion || d.NetworkEgressVersion != NetworkEgressVersion) {
		return errors.New("WireGuard 中转协议版本无效")
	}
	if d.NetworkSSHVersion != 0 && (d.NetworkSSHVersion != NetworkSSHVersion || d.NetworkEgressVersion != NetworkEgressVersion) {
		return errors.New("SSH 中转协议版本无效")
	}
	if d.NetworkGeneration < 0 || d.NetworkGeneration > 1<<53-1 || (d.NetworkGeneration > 0 && d.NetworkBindingVersion != NetworkBindingVersion) {
		return errors.New("网络配置代次或版本声明无效")
	}
	if d.Tuning.MemoryMaxMB < 0 || d.Tuning.MemoryMaxMB > 65536 || d.Tuning.GoMemLimitMB < 0 || d.Tuning.GoMemLimitMB > 65536 || d.Tuning.LimitNOFILE < 0 || d.Tuning.LimitNOFILE > 1048576 || d.Tuning.RestartSec < 0 || d.Tuning.RestartSec > 300 {
		return errors.New("调优参数超出本机允许范围")
	}
	if d.Connlog.BatchSize < 0 || d.Connlog.BatchSize > 10000 || d.Connlog.MaxBufferMB < 0 || d.Connlog.MaxBufferMB > 64 || d.Connlog.FlushSec < 0 || d.Connlog.FlushSec > 3600 {
		return errors.New("日志参数越界")
	}
	ids := map[int64]bool{}
	ports := map[int]bool{}
	// Members share their parent's listener, so the parent must be a plain
	// VLESS inbound: one user list, default egress, no port of the member's own.
	shareable := map[int64]bool{}
	for _, n := range d.Nodes {
		if n.AttachTo == 0 && n.Core == "singbox" && n.Protocol == "vless" && n.Network == nil {
			shareable[n.NodeID] = true
		}
	}
	for _, n := range d.Nodes {
		if n.AttachTo != 0 {
			if n.NodeID < 1 || ids[n.NodeID] || n.ListenPort != 0 || !shareable[n.AttachTo] || n.Core != "singbox" || n.Protocol != "vless" || n.Network != nil || n.Cert != nil {
				return errors.New("成员节点标识或所属入口无效")
			}
			if id, _ := n.Params["uuid"].(string); len(id) != 36 {
				return errors.New("成员节点缺少凭据")
			}
			if len(n.AllowFrom) > 32 {
				return errors.New("成员节点的来源地址过多")
			}
			for _, a := range n.AllowFrom {
				if _, err := netip.ParseAddr(a); err != nil {
					return errors.New("成员节点的来源地址无效")
				}
			}
			if r := n.Relay; r != nil {
				if err := r.Validate(); err != nil {
					return err
				}
			}
			if err := ValidateParams(n.Params, 0); err != nil {
				return err
			}
			ids[n.NodeID] = true
			continue
		}
		if n.NodeID < 1 || ids[n.NodeID] || n.ListenPort < 1 || n.ListenPort > 65535 || ports[n.ListenPort] || len(n.AllowFrom) > 0 || n.Relay != nil {
			return errors.New("节点标识或端口无效")
		}
		ids[n.NodeID] = true
		ports[n.ListenPort] = true
		if n.Core != "singbox" && n.Core != "snell" && n.Core != "mieru" {
			return errors.New("不支持的代理内核")
		}
		if n.Core == "snell" && n.Protocol != "snell" {
			return errors.New("协议与内核不一致")
		}
		if n.Core == "mieru" || n.Protocol == "mieru" {
			if d.MitaVersion != 1 {
				return errors.New("mita 缺少能力版本声明")
			}
			if n.Core != "mieru" || n.Protocol != "mieru" || n.ListenPort < 1025 {
				return errors.New("mieru 内核、端口或网络策略不受支持")
			}
			if _, err := mieruconfig.Decode(n.Params); err != nil {
				return err
			}
		}
		if n.Protocol == "wireguard" {
			if n.Core != "singbox" || n.Network != nil || d.NetworkWireGuardVersion != NetworkWireGuardVersion {
				return errors.New("WireGuard 接入能力或配置无效")
			}
			if _, err := wgconfig.DecodeServer(n.Params); err != nil {
				return err
			}
		}
		if err := ValidateParams(n.Params, 0); err != nil {
			return err
		}
		if n.Network != nil {
			if d.NetworkBindingVersion != NetworkBindingVersion {
				return errors.New("网络绑定配置缺少版本声明")
			}
			if n.Core != "singbox" && (n.Network.Policy.EgressProfileID != 0 || n.Network.OuterBinding() != nil || n.Network.HasTransport()) {
				return errors.New("独立内核仅支持指定监听")
			}
			if err := n.Network.Validate(); err != nil {
				return err
			}
			if !n.Blocked && n.Network.SS2022 != nil && !corecompat.SS2022Outbound(d.Versions["sing-box"].Version) {
				return errors.New(corecompat.SS2022Requirement)
			}
			if n.Network.WireGuard != nil && d.NetworkWireGuardVersion != NetworkWireGuardVersion {
				return errors.New("WireGuard 中转缺少独立版本声明")
			}
			if n.Network.SSH != nil && d.NetworkSSHVersion != NetworkSSHVersion {
				return errors.New("SSH 中转缺少独立版本声明")
			}
			if n.Network.HasTransport() && (d.NetworkEgressVersion != NetworkEgressVersion || n.Core != "singbox") {
				return errors.New("SOCKS5 出口缺少版本声明或使用了不支持的内核")
			}
		}
		if n.Cert != nil && n.Cert.Mode != "self_signed" && n.Cert.Mode != "acme" && n.Cert.Mode != "external" {
			return errors.New("证书模式无效")
		}
	}
	forwardIDs := map[int64]bool{}
	for _, f := range d.Forwards {
		if err := f.Validate(); err != nil {
			return err
		}
		if !f.Blocked && f.SS2022 != nil && !corecompat.SS2022Outbound(d.Versions["sing-box"].Version) {
			return errors.New(corecompat.SS2022Requirement)
		}
		if !f.Blocked && f.Config.Network != "tcp" && !corecompat.UDPForward(d.Versions["sing-box"].Version) {
			return errors.New(corecompat.UDPForwardUnavailable)
		}
		if forwardIDs[f.ForwardID] || ports[f.Config.ListenPort] {
			return errors.New("重复转发身份或跨资源端口冲突")
		}
		if f.HasTransport() && d.NetworkEgressVersion != NetworkEgressVersion {
			return errors.New("固定转发中转缺少出口能力声明")
		}
		if f.SSH != nil && d.NetworkSSHVersion != NetworkSSHVersion {
			return errors.New("固定转发 SSH 中转缺少能力声明")
		}
		if f.WireGuard != nil && d.NetworkWireGuardVersion != NetworkWireGuardVersion {
			return errors.New("固定转发 WireGuard 中转缺少能力声明")
		}
		forwardIDs[f.ForwardID], ports[f.Config.ListenPort] = true, true
		if !f.Blocked {
			if err := f.ValidateTargetSelection(); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateParams(v any, depth int) error {
	if depth > 16 {
		return errors.New("参数嵌套过深")
	}
	switch x := v.(type) {
	case string:
		if len(x) > 16384 || strings.ContainsAny(x, "\x00\r\n") {
			return errors.New("参数包含非法字符或过长")
		}
	case map[string]any:
		if len(x) > 128 {
			return errors.New("参数过多")
		}
		for k, v := range x {
			if len(k) > 128 || strings.ContainsAny(k, "\x00\r\n") {
				return errors.New("参数名无效")
			}
			if err := ValidateParams(v, depth+1); err != nil {
				return err
			}
		}
	case []any:
		if len(x) > 2048 {
			return errors.New("参数集合过大")
		}
		for _, v := range x {
			if err := ValidateParams(v, depth+1); err != nil {
				return err
			}
		}
	case nil, bool, int, int64, float64, json.Number:
	default:
		return errors.New("不支持的参数类型")
	}
	return nil
}

// ContentHash binds every desired-state field other than versioning metadata.
func ContentHash(d *DesiredState) string {
	cp := *d
	cp.Revision = 0
	cp.Hash = ""
	cp.GeneratedAt = time.Time{}
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var (
	relayHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	relayKey  = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	relayHex  = regexp.MustCompile(`^([0-9a-f]{2}){0,8}$`)
)

// Validate accepts only a complete, well-formed landing: a relay member with
// a broken target would silently drop its user's traffic.
func (r RelaySpec) Validate() error {
	if _, err := netip.ParseAddr(r.Server); err != nil && !relayHost.MatchString(r.Server) {
		return errors.New("中转落地地址无效")
	}
	if r.Port < 1 || r.Port > 65535 || len(r.UUID) != 36 || !relayHost.MatchString(r.ServerName) || !relayKey.MatchString(r.PublicKey) || !relayHex.MatchString(r.ShortID) {
		return errors.New("中转落地参数无效")
	}
	if r.Flow != "" && r.Flow != "xtls-rprx-vision" {
		return errors.New("中转落地流控无效")
	}
	return nil
}
