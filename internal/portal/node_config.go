package portal

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const TokenPocketNodeURL = "https://trx.mytokenpocket.vip"

// TronGrid's V1 indexer is not part of the FullNode HTTP API. Keep discovery
// and solidified receipts on the indexer service, even with a separate RPC.
func validateNodeProvider(w *WalletConfig) error {
	w.NodeProvider = strings.ToLower(strings.TrimSpace(w.NodeProvider))
	if w.NodeProvider == "" {
		w.NodeProvider = "trongrid"
	}
	w.RPCURL = strings.TrimRight(strings.TrimSpace(w.RPCURL), "/")
	if len(w.NodeAPIKey) > 4096 || strings.ContainsAny(w.NodeAPIKey, "\r\n") || len(w.RPCAPIKey) > 4096 || strings.ContainsAny(w.RPCAPIKey, "\r\n") {
		return errors.New("节点 API Key 格式不正确")
	}
	switch w.NodeProvider {
	case "tokenpocket":
		if w.Network != "mainnet" {
			return errors.New("TokenPocket 节点仅适用于 TRON 主网；测试网请选择其他服务商")
		}
		w.RPCURL, w.RPCAPIKey, w.RPCURLHost, w.RPCAPIHeader = "", "", "", ""
		return nil
	case "trongrid":
		w.RPCURL, w.RPCAPIKey, w.RPCURLHost, w.RPCAPIHeader = "", "", "", ""
		return nil
	case "nownodes":
		if w.Network != "mainnet" {
			return errors.New("NOWNodes 预设仅支持 TRON 主网")
		}
		w.RPCAPIHeader = "api-key"
	case "quicknode", "getblock":
		if w.Network == "shasta" {
			return errors.New("所选服务商预设支持主网与 Nile；Shasta 请选择 TronGrid 或自定义节点")
		}
		// These services authenticate using the private endpoint's path.
		w.RPCAPIHeader, w.RPCAPIKey = "", ""
	case "custom":
		switch w.RPCAPIHeader {
		case "", "TRON-PRO-API-KEY", "api-key", "X-API-Key", "Authorization":
		default:
			return errors.New("请选择支持的 RPC 认证请求头")
		}
		if w.RPCAPIHeader == "" && w.RPCAPIKey != "" {
			return errors.New("填写 RPC API Key 时请选择对应的认证请求头")
		}
	default:
		return errors.New("请选择支持的 TRON 节点服务商")
	}
	if w.RPCURL == "" {
		return nil // A saved, encrypted endpoint may be retained on an ordinary save.
	}
	u, err := url.Parse(w.RPCURL)
	if err != nil || len(w.RPCURL) > 4096 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return errors.New("RPC 端点须为完整 HTTPS 地址；凭据可放在路径中，不支持账号密码、查询串或锚点")
	}
	switch w.NodeProvider {
	case "nownodes":
		if u.Host != "trx.nownodes.io" || u.Path != "" {
			return errors.New("NOWNodes RPC 地址应为 https://trx.nownodes.io")
		}
	case "quicknode":
		if !strings.HasSuffix(u.Hostname(), ".quiknode.pro") || u.Port() != "" || strings.Trim(u.Path, "/") == "" {
			return errors.New("请填写 QuickNode 控制台提供的完整 TRON HTTPS 端点（含路径令牌）")
		}
	case "getblock":
		if u.Host != "go.getblock.io" || strings.Trim(u.Path, "/") == "" {
			return errors.New("请填写 GetBlock 的 TRON REST FullNode HTTPS 端点（含访问令牌）")
		}
	}
	w.RPCURLHost = u.Host
	return nil
}

func nodeEndpoint(w WalletConfig, path string) (base, key, header string, err error) {
	if strings.HasPrefix(path, "/walletsolidity/") {
		return strings.TrimRight(w.NodeURL, "/"), w.NodeAPIKey, "TRON-PRO-API-KEY", nil
	}
	if !strings.HasPrefix(path, "/wallet/") {
		return "", "", "", errors.New("不支持的 TRON RPC 路径")
	}
	switch w.NodeProvider {
	case "tokenpocket":
		if w.Network != "mainnet" {
			return "", "", "", errors.New("TokenPocket 节点仅适用于 TRON 主网")
		}
		return TokenPocketNodeURL, "", "", nil
	case "", "trongrid":
		return strings.TrimRight(w.NodeURL, "/"), w.NodeAPIKey, "TRON-PRO-API-KEY", nil
	case "nownodes", "quicknode", "getblock", "custom":
		if w.RPCURL == "" {
			return "", "", "", errors.New("请先保存所选服务商的 RPC 端点")
		}
		if w.NodeProvider == "nownodes" && w.RPCAPIKey == "" {
			return "", "", "", errors.New("请先配置 NOWNodes API Key")
		}
		return strings.TrimRight(w.RPCURL, "/"), w.RPCAPIKey, w.RPCAPIHeader, nil
	default:
		return "", "", "", errors.New("节点服务商配置不正确")
	}
}

type nodeProbeCheck struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Message     string `json:"message"`
	LatencyMS   int64  `json:"latency_ms"`
	BlockHeight int64  `json:"block_height,omitempty"`
}

type nodeProbeResult struct {
	Passed    bool             `json:"passed"`
	Network   string           `json:"network"`
	Provider  string           `json:"provider"`
	CheckedAt time.Time        `json:"checked_at"`
	Checks    []nodeProbeCheck `json:"checks"`
}

func (s *Service) probeNodes(ctx context.Context, w WalletConfig) nodeProbeResult {
	result := nodeProbeResult{Passed: true, Network: w.Network, Provider: w.NodeProvider, CheckedAt: time.Now(), Checks: []nodeProbeCheck{}}
	for _, item := range []struct{ name, path string }{
		{"FullNode RPC", "/wallet/getnowblock"},
		{"固化区块查询", "/walletsolidity/getnowblock"},
		{"TRC20 交易索引", ""},
	} {
		started := time.Now()
		check := nodeProbeCheck{Name: item.name}
		var err error
		if item.path == "" {
			// This public burn address is deliberately unrelated to any customer.
			var out struct {
				Success bool              `json:"success"`
				Data    []json.RawMessage `json:"data"`
			}
			err = s.externalJSON(ctx, "GET", strings.TrimRight(w.NodeURL, "/")+"/v1/accounts/T9yD14Nj9j7xAB4dbGeiX9h8unkKHxuWwb/transactions/trc20?limit=1&only_confirmed=true", w.NodeAPIKey, "TRON-PRO-API-KEY", nil, &out)
			if err == nil && (!out.Success || out.Data == nil) {
				err = errors.New("未返回 TronGrid V1 交易列表，请确认索引服务地址和权限")
			}
		} else {
			var out struct {
				ID     string `json:"blockID"`
				Header struct {
					Raw struct {
						Number int64 `json:"number"`
					} `json:"raw_data"`
				} `json:"block_header"`
			}
			err = s.node(ctx, w, item.path, map[string]any{}, &out)
			if err == nil {
				id, decodeErr := hex.DecodeString(out.ID)
				if decodeErr != nil || len(id) != 32 || out.Header.Raw.Number <= 0 {
					err = errors.New("未返回有效区块，请确认选择的是 TRON HTTP REST 节点而非 JSON-RPC")
				} else {
					check.BlockHeight = out.Header.Raw.Number
				}
			}
		}
		check.LatencyMS = time.Since(started).Milliseconds()
		check.Passed = err == nil
		check.Message = "连接正常"
		if err != nil {
			// Do not include provider response bodies or private endpoint URLs.
			check.Message = "检测失败：" + err.Error()
			result.Passed = false
		}
		result.Checks = append(result.Checks, check)
	}
	return result
}

func (s *Service) probeWalletNodes(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	if !s.allow("wallet-probe:"+currentUser(r).ID, 3, time.Minute) {
		fail(w, 429, "节点检测较频繁，请一分钟后重试")
		return
	}
	cfg, err := s.Settings(r.Context(), true)
	if err != nil {
		s.dbError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	jsonReply(w, 200, s.probeNodes(ctx, cfg.Wallet))
}
