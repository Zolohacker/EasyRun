package portal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

func (s *Service) externalJSON(ctx context.Context, method, address, key, header string, input, output any) error {
	var body []byte
	var e error
	if input != nil {
		body, e = json.Marshal(input)
		if e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EasyRun/2.0")
	if key != "" {
		req.Header.Set(header, key)
	}
	s.networkMu.Lock()
	delay := time.Until(s.nextRequest)
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.networkMu.Unlock()
			return ctx.Err()
		case <-timer.C:
		}
	}
	s.nextRequest = time.Now().Add(350 * time.Millisecond)
	s.networkMu.Unlock()
	resp, e := s.http.Do(req)
	if e != nil {
		return fmt.Errorf("外部服务连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("外部服务 HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output)
}
func (s *Service) node(ctx context.Context, w WalletConfig, path string, in, out any) error {
	return s.externalJSON(ctx, "POST", w.NodeURL+path, w.NodeAPIKey, "TRON-PRO-API-KEY", in, out)
}
func abiAddress(address string) (string, error) {
	raw, e := addressBytes(address)
	if e != nil {
		return "", e
	}
	return strings.Repeat("0", 24) + hex.EncodeToString(raw[1:]), nil
}
func transferABI(address string, amount int64) (string, error) {
	if amount <= 0 {
		return "", errors.New("invalid transfer amount")
	}
	a, e := abiAddress(address)
	if e != nil {
		return "", e
	}
	return a + fmt.Sprintf("%064x", amount), nil
}
func (s *Service) balance(ctx context.Context, w WalletConfig, address string) (int64, error) {
	param, e := abiAddress(address)
	if e != nil {
		return 0, e
	}
	var out struct {
		Result struct {
			Result bool `json:"result"`
		} `json:"result"`
		Values []string `json:"constant_result"`
	}
	e = s.node(ctx, w, "/wallet/triggerconstantcontract", map[string]any{"owner_address": address, "contract_address": w.Contract, "function_selector": "balanceOf(address)", "parameter": param, "visible": true}, &out)
	if e != nil {
		return 0, e
	}
	if !out.Result.Result || len(out.Values) != 1 {
		return 0, errors.New("余额查询失败")
	}
	n, ok := new(big.Int).SetString(out.Values[0], 16)
	if !ok || !n.IsInt64() || n.Sign() < 0 {
		return 0, errors.New("余额超出支持范围")
	}
	return n.Int64(), nil
}

type tronLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}
type tronReceipt struct {
	ID          string `json:"id"`
	BlockNumber int64  `json:"blockNumber"`
	Result      string `json:"result"`
	Receipt     struct {
		Result string `json:"result"`
	} `json:"receipt"`
	Logs []tronLog `json:"log"`
}

func (s *Service) receipt(ctx context.Context, w WalletConfig, id string) (tronReceipt, error) {
	var receipt tronReceipt
	if len(id) != 64 {
		return receipt, errors.New("invalid transaction id")
	}
	e := s.node(ctx, w, "/walletsolidity/gettransactioninfobyid", map[string]string{"value": id}, &receipt)
	return receipt, e
}

const transferTopic = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

func receivedAmount(log tronLog, contract, address string) (int64, bool) {
	c, e := addressBytes(contract)
	if e != nil {
		return 0, false
	}
	a, e := addressBytes(address)
	if e != nil {
		return 0, false
	}
	logAddress := strings.TrimPrefix(log.Address, "0x")
	if len(logAddress) == 42 && strings.HasPrefix(logAddress, "41") {
		logAddress = logAddress[2:]
	}
	if !strings.EqualFold(logAddress, hex.EncodeToString(c[1:])) || len(log.Topics) != 3 || !strings.EqualFold(log.Topics[0], transferTopic) || !strings.EqualFold(log.Topics[2], strings.Repeat("0", 24)+hex.EncodeToString(a[1:])) {
		return 0, false
	}
	n, ok := new(big.Int).SetString(log.Data, 16)
	if !ok || !n.IsInt64() || n.Sign() <= 0 {
		return 0, false
	}
	return n.Int64(), true
}

// parseWire accepts the bounded protobuf subset used by a TRON transaction.
// Duplicate fields and unsupported wire types are rejected before signing.
type wireField struct {
	data   []byte
	number uint64
	kind   int
}

func parseWire(raw []byte) (map[int]wireField, error) {
	out := map[int]wireField{}
	for len(raw) > 0 {
		tag, n := binary.Uvarint(raw)
		if n <= 0 {
			return nil, errors.New("invalid protobuf tag")
		}
		raw = raw[n:]
		field, kind := int(tag>>3), int(tag&7)
		if field < 1 {
			return nil, errors.New("invalid protobuf field")
		}
		if _, ok := out[field]; ok {
			return nil, errors.New("duplicate protobuf field")
		}
		v := wireField{kind: kind}
		switch kind {
		case 0:
			v.number, n = binary.Uvarint(raw)
			if n <= 0 {
				return nil, errors.New("invalid protobuf varint")
			}
			raw = raw[n:]
		case 2:
			var size uint64
			size, n = binary.Uvarint(raw)
			if n <= 0 || size > uint64(len(raw)-n) {
				return nil, errors.New("invalid protobuf length")
			}
			raw = raw[n:]
			v.data = raw[:int(size)]
			raw = raw[int(size):]
		default:
			return nil, errors.New("unsupported protobuf wire type")
		}
		out[field] = v
	}
	return out, nil
}
func validateTransferRaw(raw []byte, w WalletConfig, owner, destination string, amount int64, now time.Time) error {
	f, e := parseWire(raw)
	if e != nil {
		return e
	}
	allowed := map[int]bool{1: true, 3: true, 4: true, 8: true, 11: true, 14: true, 18: true}
	for k := range f {
		if !allowed[k] {
			return errors.New("unexpected raw transaction field")
		}
	}
	if f[1].kind != 2 || len(f[1].data) != 2 || f[4].kind != 2 || len(f[4].data) != 8 || f[8].kind != 0 || f[8].number < uint64(now.Add(10*time.Second).UnixMilli()) || f[8].number > uint64(now.Add(24*time.Hour).UnixMilli()) || f[18].kind != 0 || f[18].number != uint64(w.FeeLimitSun) {
		return errors.New("transaction expiry, reference or fee limit mismatch")
	}
	c, e := parseWire(f[11].data)
	if e != nil {
		return e
	}
	for k := range c {
		if k != 1 && k != 2 {
			return errors.New("unexpected transaction permission or contract field")
		}
	}
	if c[1].number != 31 || c[1].kind != 0 || c[2].kind != 2 {
		return errors.New("only TriggerSmartContract may be signed")
	}
	p, e := parseWire(c[2].data)
	if e != nil {
		return e
	}
	if len(p) != 2 || string(p[1].data) != "type.googleapis.com/protocol.TriggerSmartContract" || p[2].kind != 2 {
		return errors.New("unexpected contract type URL")
	}
	v, e := parseWire(p[2].data)
	if e != nil {
		return e
	}
	for k, x := range v {
		if k != 1 && k != 2 && k != 4 {
			if (k == 3 || k == 5 || k == 6) && x.kind == 0 && x.number == 0 {
				continue
			}
			return errors.New("unexpected TRX or TRC10 transfer")
		}
	}
	o, e := addressBytes(owner)
	if e != nil {
		return e
	}
	contract, e := addressBytes(w.Contract)
	if e != nil {
		return e
	}
	abi, e := transferABI(destination, amount)
	if e != nil {
		return e
	}
	expected, _ := hex.DecodeString("a9059cbb" + abi)
	if !bytes.Equal(v[1].data, o) || !bytes.Equal(v[2].data, contract) || !bytes.Equal(v[4].data, expected) {
		return errors.New("unsigned transaction owner, token or recipient mismatch")
	}
	return nil
}
func (s *Service) signedTransfer(ctx context.Context, w WalletConfig, key *secp.PrivateKey, destination string, amount int64) (map[string]any, string, error) {
	owner := publicAddress(key)
	abi, e := transferABI(destination, amount)
	if e != nil {
		return nil, "", e
	}
	var out struct {
		Result struct {
			Result bool `json:"result"`
		} `json:"result"`
		Transaction map[string]any `json:"transaction"`
	}
	e = s.node(ctx, w, "/wallet/triggersmartcontract", map[string]any{"owner_address": owner, "contract_address": w.Contract, "function_selector": "transfer(address,uint256)", "parameter": abi, "fee_limit": w.FeeLimitSun, "call_value": 0, "visible": true}, &out)
	if e != nil {
		return nil, "", e
	}
	if !out.Result.Result {
		return nil, "", errors.New("构造归集交易失败")
	}
	rawHex, _ := out.Transaction["raw_data_hex"].(string)
	raw, e := hex.DecodeString(rawHex)
	if e != nil {
		return nil, "", e
	}
	if e = validateTransferRaw(raw, w, owner, destination, amount, time.Now()); e != nil {
		return nil, "", e
	}
	hash := sha256.Sum256(raw)
	id := hex.EncodeToString(hash[:])
	if out.Transaction["txID"] != id {
		return nil, "", errors.New("transaction ID mismatch")
	}
	compact := ecdsa.SignCompact(key, hash[:], false)
	signature := append(append([]byte{}, compact[1:]...), compact[0]-27)
	out.Transaction["signature"] = []string{hex.EncodeToString(signature)}
	return out.Transaction, id, nil
}
