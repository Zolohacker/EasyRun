package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"
)

func (s *Service) generateSeed(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !s.passwordOK(r, in.Password) {
		fail(w, 403, "请填写当前超管密码")
		return
	}
	c, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if c.Wallet.SeedConfigured {
		fail(w, 409, "HD seed 已存在；请使用先前的离线备份")
		return
	}
	seed := make([]byte, 64)
	if _, e = rand.Read(seed); e != nil {
		s.dbError(w, e)
		return
	}
	c.Wallet.SeedHex = hex.EncodeToString(seed)
	if e = s.saveSettings(r, c); e != nil {
		fail(w, 400, e.Error())
		return
	}
	s.audit(r.Context(), "wallet.seed-create", "HD wallet")
	jsonReply(w, 201, map[string]string{"seed_hex": c.Wallet.SeedHex, "warning": "此 seed 仅展示一次。请离线备份并同时备份 ACCOUNT_MASTER_KEY；勿向他人发送。"})
}
func (s *Service) depositAddress(w http.ResponseWriter, r *http.Request) {
	cfg, e := s.Settings(r.Context(), true)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if !cfg.Wallet.Enabled || !cfg.Wallet.SeedConfigured {
		fail(w, 400, "自助充值尚未开启")
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	_, e = tx.Exec(r.Context(), `SELECT id FROM portal_settings WHERE id=1 FOR UPDATE`)
	if e != nil {
		s.dbError(w, e)
		return
	}
	a := DepositAddress{UserID: currentUser(r).ID}
	e = tx.QueryRow(r.Context(), `SELECT address,derivation_index,created_at FROM portal_wallet_addresses WHERE user_id=$1`, a.UserID).Scan(&a.Address, &a.Index, &a.CreatedAt)
	if e == nil {
		jsonReply(w, 200, a)
		return
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		s.dbError(w, e)
		return
	}
	if e = tx.QueryRow(r.Context(), `SELECT nextval('portal_wallet_index_seq')`).Scan(&a.Index); e != nil {
		s.dbError(w, e)
		return
	}
	seed, e := decodeSeed(cfg.Wallet.SeedHex)
	if e != nil {
		s.dbError(w, e)
		return
	}
	key, e := deriveKey(seed, a.Index)
	clear(seed)
	if e != nil {
		s.dbError(w, e)
		return
	}
	a.Address = publicAddress(key)
	key.Zero()
	e = tx.QueryRow(r.Context(), `INSERT INTO portal_wallet_addresses(user_id,address,derivation_index)VALUES($1,$2,$3) RETURNING created_at`, a.UserID, a.Address, a.Index).Scan(&a.CreatedAt)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 201, a)
}
func (s *Service) scanNow(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	select {
	case s.scanRequested <- struct{}{}:
	default:
	}
	jsonReply(w, 202, map[string]string{"status": "queued"})
}
func (s *Service) collectNow(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, 400, e.Error())
		return
	}
	if !s.passwordOK(r, in.Password) {
		fail(w, 403, "请填写当前超管密码")
		return
	}
	cfg, e := s.Settings(r.Context(), false)
	if e != nil {
		s.dbError(w, e)
		return
	}
	if !cfg.Wallet.Enabled || cfg.Wallet.CollectionAddress == "" || !cfg.Wallet.SeedConfigured {
		fail(w, 400, "请先配置钱包和归集地址")
		return
	}
	if cfg.Wallet.Network == "mainnet" && !cfg.Wallet.FeeeAPIKeyConfigured {
		fail(w, 400, "主网归集需要先配置 Feee API key")
		return
	}
	select {
	case s.collectRequested <- struct{}{}:
	default:
	}
	s.audit(r.Context(), "wallet.collect-request", cfg.Wallet.CollectionAddress)
	jsonReply(w, 202, map[string]string{"status": "queued"})
}
func (s *Service) listSweeps(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	rows, e := s.db.Query(r.Context(), `SELECT id,address,amount_micro,state,tx_id,message,created_at FROM portal_sweeps ORDER BY created_at DESC LIMIT 200`)
	if e != nil {
		s.dbError(w, e)
		return
	}
	defer rows.Close()
	out := []Sweep{}
	for rows.Next() {
		var v Sweep
		if e = rows.Scan(&v.ID, &v.Address, &v.AmountMicro, &v.State, &v.TxID, &v.Message, &v.CreatedAt); e != nil {
			s.dbError(w, e)
			return
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		s.dbError(w, e)
		return
	}
	jsonReply(w, 200, out)
}

// Run owns all blockchain I/O. It starts disabled until the administrator
// explicitly configures a network, token contract, seed and enabled=true.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	lastScan := time.Time{}
	lastMaintenance := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.scanRequested:
			lastScan = time.Time{}
		case <-s.collectRequested:
			s.collect(ctx)
		}
		cfg, e := s.Settings(ctx, true)
		if e != nil {
			continue
		}
		if time.Since(lastMaintenance) >= time.Minute {
			s.maintenance(ctx)
			lastMaintenance = time.Now()
		}
		if !cfg.Wallet.Enabled {
			continue
		}
		if time.Since(lastScan) >= time.Minute {
			if e = s.scanDeposits(ctx, cfg.Wallet); e != nil {
				slog.Warn("scan wallet deposits", "error", e)
			}
			lastScan = time.Now()
		}
		s.reconcileSweeps(ctx, cfg.Wallet)
		if cfg.Wallet.AutoCollect && s.claimCollectionTime(ctx, cfg.Wallet) {
			s.collect(ctx)
		}
	}
}

// Repair a transient finalization failure only when the persisted execution
// has a definite outcome. Still-running/ambiguous executions keep their hold.
func (s *Service) maintenance(ctx context.Context) {
	_, _ = s.db.Exec(ctx, `DELETE FROM portal_sessions WHERE expires_at<now(); DELETE FROM portal_email_codes WHERE expires_at<now()-interval '1 day';`)
	rows, e := s.db.Query(ctx, `SELECT c.execution_id,e.status,e.remote_run_id FROM portal_charges c JOIN automation_executions e ON e.id=c.execution_id WHERE c.state='reserved' AND e.status IN('succeeded','failed','skipped') LIMIT 200`)
	if e != nil {
		return
	}
	type item struct{ ID, Status, Remote string }
	items := []item{}
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.ID, &i.Status, &i.Remote); e != nil {
			rows.Close()
			return
		}
		items = append(items, i)
	}
	rows.Close()
	for _, i := range items {
		if e = s.Finish(ctx, i.ID, i.Status == "succeeded" || i.Remote != ""); e != nil {
			slog.Warn("reconcile finalized charge", "execution_id", i.ID, "error", e)
		}
	}
}
func (s *Service) claimCollectionTime(ctx context.Context, w WalletConfig) bool {
	schedule, e := cron.ParseStandard("CRON_TZ=" + w.Timezone + " " + w.CollectCron)
	if e != nil {
		return false
	}
	next := schedule.Next(time.Now())
	// First enablement initializes the next scheduled instant without moving funds.
	tag, e := s.db.Exec(ctx, `UPDATE portal_settings SET next_collect_at=$1 WHERE id=1 AND next_collect_at IS NULL`, next)
	if e != nil || tag.RowsAffected() > 0 {
		return false
	}
	tag, e = s.db.Exec(ctx, `UPDATE portal_settings SET next_collect_at=$1 WHERE id=1 AND next_collect_at<=now()`, next)
	if e != nil || tag.RowsAffected() == 0 {
		return false
	}
	var previous bool
	_ = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_sweeps WHERE created_at>now()-interval '1 minute')`).Scan(&previous)
	return !previous
}
func (s *Service) locked(ctx context.Context, id int64, fn func() error) error {
	conn, e := s.db.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	var ok bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, id).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return nil
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, id)
	return fn()
}
func (s *Service) scanDeposits(ctx context.Context, w WalletConfig) error {
	return s.locked(ctx, 42831002, func() error {
		rows, e := s.db.Query(ctx, `SELECT user_id,address,derivation_index,created_at,scanned_at,full_scanned_at FROM portal_wallet_addresses ORDER BY derivation_index`)
		if e != nil {
			return e
		}
		type addr struct {
			DepositAddress
			Scanned     *time.Time
			FullScanned *time.Time
		}
		list := []addr{}
		for rows.Next() {
			var a addr
			if e = rows.Scan(&a.UserID, &a.Address, &a.Index, &a.CreatedAt, &a.Scanned, &a.FullScanned); e != nil {
				rows.Close()
				return e
			}
			list = append(list, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, a := range list {
			since := a.CreatedAt
			fullScan := a.FullScanned == nil || time.Since(*a.FullScanned) >= 24*time.Hour
			if a.Scanned != nil && !fullScan {
				since = a.Scanned.Add(-15 * time.Minute)
				if since.Before(a.CreatedAt) {
					since = a.CreatedAt
				}
			}
			scanned := time.Now()
			if e = s.scanAddress(ctx, w, a.DepositAddress, since); e != nil {
				return e
			}
			if _, e = s.db.Exec(ctx, `UPDATE portal_wallet_addresses SET scanned_at=$2,full_scanned_at=CASE WHEN $3 THEN $2 ELSE full_scanned_at END WHERE user_id=$1`, a.UserID, scanned, fullScan); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Service) scanAddress(ctx context.Context, w WalletConfig, a DepositAddress, since time.Time) error {
	params := url.Values{"only_confirmed": {"true"}, "only_to": {"true"}, "contract_address": {w.Contract}, "limit": {"200"}, "order_by": {"block_timestamp,asc"}, "min_timestamp": {strconv.FormatInt(since.UnixMilli(), 10)}}
	seen := map[string]bool{}
	for page := 0; page < 100; page++ {
		var out struct {
			Success bool `json:"success"`
			Data    []struct {
				Tx string `json:"transaction_id"`
			} `json:"data"`
			Meta struct {
				Fingerprint string `json:"fingerprint"`
				Links       struct {
					Next string `json:"next"`
				} `json:"links"`
			} `json:"meta"`
		}
		e := s.externalJSON(ctx, "GET", w.NodeURL+"/v1/accounts/"+a.Address+"/transactions/trc20?"+params.Encode(), w.NodeAPIKey, "TRON-PRO-API-KEY", nil, &out)
		if e != nil {
			return e
		}
		if !out.Success {
			return errors.New("TRON 交易索引查询失败")
		}
		for _, t := range out.Data {
			if seen[t.Tx] {
				continue
			}
			seen[t.Tx] = true
			receipt, e := s.receipt(ctx, w, t.Tx)
			if e != nil {
				return e
			}
			if receipt.ID != t.Tx || receipt.BlockNumber <= 0 || receipt.Result == "FAILED" || receipt.Receipt.Result != "SUCCESS" {
				continue
			}
			for i, log := range receipt.Logs {
				if amount, ok := receivedAmount(log, w.Contract, a.Address); ok {
					if e = s.creditDeposit(ctx, a, t.Tx, i, amount); e != nil {
						return e
					}
				}
			}
		}
		if out.Meta.Fingerprint == "" || out.Meta.Links.Next == "" {
			return nil
		}
		params.Set("fingerprint", out.Meta.Fingerprint)
	}
	return errors.New("充值扫描分页上限已到，将保留游标重试")
}
func (s *Service) creditDeposit(ctx context.Context, a DepositAddress, txid string, index int, amount int64) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	tag, e := tx.Exec(ctx, `INSERT INTO portal_deposits(tx_id,event_index,user_id,amount_micro,address)VALUES($1,$2,$3,$4,$5)ON CONFLICT DO NOTHING`, txid, index, a.UserID, amount, a.Address)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	_, e = tx.Exec(ctx, `UPDATE portal_users SET balance_micro=balance_micro+$2 WHERE id=$1`, a.UserID, amount)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference)VALUES($1,$2,$3,'trc20_deposit',$4)`, platform.NewID("led"), a.UserID, amount, fmt.Sprintf("deposit:%s:%d", txid, index))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Service) sweepState(ctx context.Context, id, state, message string) {
	if _, e := s.db.Exec(ctx, `UPDATE portal_sweeps SET state=$2,message=$3,updated_at=now() WHERE id=$1 AND state<>'confirmed'`, id, state, message); e != nil {
		slog.Error("save sweep state", "error", e)
	}
}
func (s *Service) collect(ctx context.Context) {
	err := s.locked(ctx, 42831003, func() error {
		cfg, e := s.Settings(ctx, true)
		if e != nil {
			return e
		}
		w := cfg.Wallet
		if !w.Enabled || !ValidAddress(w.CollectionAddress) {
			return errors.New("归集未配置")
		}
		var own bool
		if e = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portal_wallet_addresses WHERE address=$1)`, w.CollectionAddress).Scan(&own); e != nil {
			return e
		}
		if own {
			return errors.New("归集地址不可为客户充值地址")
		}
		rows, e := s.db.Query(ctx, `SELECT user_id,address,derivation_index,created_at FROM portal_wallet_addresses ORDER BY derivation_index`)
		if e != nil {
			return e
		}
		addresses := []DepositAddress{}
		for rows.Next() {
			var a DepositAddress
			if e = rows.Scan(&a.UserID, &a.Address, &a.Index, &a.CreatedAt); e != nil {
				rows.Close()
				return e
			}
			addresses = append(addresses, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, a := range addresses {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			amount, e := s.balance(ctx, w, a.Address)
			if e != nil {
				slog.Warn("read collection balance", "address", a.Address, "error", e)
				continue
			}
			if amount < w.MinCollectMicro {
				continue
			}
			id := platform.NewID("sweep")
			tag, e := s.db.Exec(ctx, `INSERT INTO portal_sweeps(id,address,amount_micro,state,destination)VALUES($1,$2,$3,'queued',$4)ON CONFLICT DO NOTHING`, id, a.Address, amount, w.CollectionAddress)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			if e = s.collectAddress(ctx, w, a, id, amount); e != nil {
				s.sweepState(context.WithoutCancel(ctx), id, "needs_review", e.Error())
			}
		}
		return nil
	})
	if err != nil {
		slog.Warn("wallet collection", "error", err)
	}
}
func (s *Service) collectAddress(ctx context.Context, w WalletConfig, a DepositAddress, id string, amount int64) error {
	var acct struct {
		Address string `json:"address"`
		Balance int64  `json:"balance"`
	}
	if e := s.node(ctx, w, "/wallet/getaccount", map[string]any{"address": a.Address, "visible": true}, &acct); e != nil {
		return e
	}
	if acct.Address == "" {
		return errors.New("充值地址尚未激活，请先转入少量 TRX 以激活并覆盖带宽费用")
	}
	if w.Network == "mainnet" {
		if w.FeeeAPIKey == "" {
			return errors.New("请配置 Feee API key")
		}
		if e := s.rentEnergy(ctx, w, a.Address, id); e != nil {
			return e
		}
	}
	seed, e := decodeSeed(w.SeedHex)
	if e != nil {
		return e
	}
	key, e := deriveKey(seed, a.Index)
	clear(seed)
	if e != nil {
		return e
	}
	defer key.Zero()
	if publicAddress(key) != a.Address {
		return errors.New("HD 派生地址不匹配，已停止签名")
	}
	signed, txid, e := s.signedTransfer(ctx, w, key, w.CollectionAddress, amount)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(signed)
	if e != nil {
		return e
	}
	_, e = s.db.Exec(ctx, `UPDATE portal_sweeps SET state='signed',tx_id=$2,signed_tx=$3,message='已签名，等待广播',updated_at=now()WHERE id=$1`, id, txid, raw)
	if e != nil {
		return e
	}
	s.broadcast(ctx, w, id, signed)
	return nil
}
func (s *Service) rentEnergy(ctx context.Context, w WalletConfig, address, id string) error {
	var estimate struct {
		Code int `json:"code"`
		Data struct {
			Used int64 `json:"energy_used"`
		} `json:"data"`
	}
	params := url.Values{"from_address": {address}, "to_address": {w.CollectionAddress}, "contract_address": {w.Contract}}
	if e := s.externalJSON(ctx, "GET", "https://feee.io/open/v2/order/estimate_energy?"+params.Encode(), w.FeeeAPIKey, "key", nil, &estimate); e != nil {
		return e
	}
	if estimate.Code != 0 || estimate.Data.Used <= 0 || estimate.Data.Used > 5_000_000 {
		return errors.New("Feee 能量估算失败")
	}
	energy := (estimate.Data.Used*110/100 + 999) / 1000 * 1000
	var price struct {
		Code int `json:"code"`
		Data struct {
			Sun float64 `json:"price_in_sun"`
		} `json:"data"`
	}
	query := url.Values{"resource_value": {fmt.Sprint(energy)}, "rent_duration": {"10"}, "rent_time_unit": {"m"}}
	if e := s.externalJSON(ctx, "GET", "https://feee.io/open/v2/order/price?"+query.Encode(), w.FeeeAPIKey, "key", nil, &price); e != nil {
		return e
	}
	if price.Code != 0 || price.Data.Sun <= 0 || price.Data.Sun > float64(w.EnergyPriceSun) {
		return errors.New("Feee 能量报价超过所配置的单价上限")
	}
	s.sweepState(ctx, id, "renting", "能量租赁请求已提交；请求不明时不会自动重复下单")
	var order struct {
		Code int `json:"code"`
		Data struct {
			Order string `json:"order_no"`
		} `json:"data"`
	}
	if e := s.externalJSON(ctx, "POST", "https://feee.io/open/v2/order/submit", w.FeeeAPIKey, "key", map[string]any{"resource_type": 1, "receive_address": address, "resource_value": energy, "rent_duration": 10, "rent_time_unit": "m", "rent_time_second": 600}, &order); e != nil {
		return fmt.Errorf("能量订单结果待核对，禁止自动重下：%w", e)
	}
	if order.Code != 0 || order.Data.Order == "" {
		return errors.New("Feee 下单未成功，请检查 API 余额和白名单")
	}
	s.sweepState(ctx, id, "renting", "Feee 订单 "+order.Data.Order+"，等待链上能量到账")
	for i := 0; i < 40; i++ {
		var resource struct {
			Limit int64 `json:"EnergyLimit"`
			Used  int64 `json:"EnergyUsed"`
		}
		if e := s.node(ctx, w, "/wallet/getaccountresource", map[string]any{"address": address, "visible": true}, &resource); e != nil {
			return e
		}
		if resource.Limit-resource.Used >= estimate.Data.Used {
			return nil
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("等待能量到账超时，请核对订单后再处理")
}
func (s *Service) broadcast(ctx context.Context, w WalletConfig, id string, signed map[string]any) {
	var out struct {
		Result bool   `json:"result"`
		Code   string `json:"code"`
	}
	e := s.node(ctx, w, "/wallet/broadcasttransaction", signed, &out)
	if e != nil {
		s.sweepState(ctx, id, "signed", "广播结果待核对；将仅重播同一交易，不重新签名")
		return
	}
	if !out.Result && out.Code != "DUP_TRANSACTION_ERROR" {
		s.sweepState(ctx, id, "signed", "节点暂未接受交易："+out.Code)
		return
	}
	s.sweepState(ctx, id, "broadcast", "已广播，等待固化及 Transfer 事件确认")
}
func (s *Service) reconcileSweeps(ctx context.Context, w WalletConfig) {
	_ = s.locked(ctx, 42831004, func() error {
		_, _ = s.db.Exec(ctx, `UPDATE portal_sweeps SET state='needs_review',message='服务曾中断，原能量订单结果需要人工核对',updated_at=now() WHERE state IN('queued','renting') AND updated_at<now()-interval '15 minutes'`)
		rows, e := s.db.Query(ctx, `SELECT id,address,tx_id,amount_micro,state,signed_tx,destination FROM portal_sweeps WHERE state IN('signed','broadcast') ORDER BY created_at LIMIT 100`)
		if e != nil {
			return e
		}
		type pending struct {
			ID, Address, TxID string
			Amount            int64
			State             string
			Signed            []byte
			Destination       string
		}
		items := []pending{}
		for rows.Next() {
			var p pending
			if e = rows.Scan(&p.ID, &p.Address, &p.TxID, &p.Amount, &p.State, &p.Signed, &p.Destination); e != nil {
				rows.Close()
				return e
			}
			items = append(items, p)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, p := range items {
			receipt, e := s.receipt(ctx, w, p.TxID)
			if e != nil {
				continue
			}
			if receipt.ID == p.TxID && receipt.BlockNumber > 0 {
				if receipt.Result == "FAILED" || receipt.Receipt.Result != "SUCCESS" {
					s.sweepState(ctx, p.ID, "failed", "链上交易执行失败，请检查能量和带宽")
					continue
				}
				matched := false
				for _, l := range receipt.Logs {
					if amount, ok := receivedAmount(l, w.Contract, p.Destination); ok && amount == p.Amount {
						matched = true
					}
				}
				if matched {
					s.sweepState(ctx, p.ID, "confirmed", "归集已在链上固化确认")
				} else {
					s.sweepState(ctx, p.ID, "needs_review", "交易已固化但 Transfer 事件未匹配，请人工核对")
				}
				continue
			}
			var signed map[string]any
			if json.Unmarshal(p.Signed, &signed) != nil {
				continue
			}
			raw, _ := signed["raw_data"].(map[string]any)
			expiry, _ := raw["expiration"].(float64)
			if expiry > 0 && time.Now().UnixMilli() > int64(expiry) {
				s.sweepState(ctx, p.ID, "needs_review", "原交易已过期且固化记录暂未找到；保留 txID 防止重复转账，请人工核对")
				continue
			}
			s.broadcast(ctx, w, p.ID, signed)
		}
		return nil
	})
}
