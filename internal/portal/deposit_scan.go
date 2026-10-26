package portal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const depositScanCooldown = time.Minute

type depositScanState struct {
	State             string     `json:"state"`
	Message           string     `json:"message"`
	RequestedAt       *time.Time `json:"requested_at,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	NextAllowedAt     *time.Time `json:"next_allowed_at,omitempty"`
	ServerTime        time.Time  `json:"server_time"`
	RetryAfterSeconds int        `json:"retry_after_seconds"`
	CreditedMicro     int64      `json:"credited_micro"`
	DepositCount      int        `json:"deposit_count"`
}

func (s *Service) depositScanStatus(ctx context.Context, userID string) (depositScanState, error) {
	var v depositScanState
	err := s.db.QueryRow(ctx, `SELECT clock_timestamp(),coalesce(q.state,'idle'),coalesce(q.message,''),q.requested_at,q.started_at,q.finished_at,coalesce(q.credited_micro,0),coalesce(q.deposit_count,0)
		FROM (SELECT $1::text AS user_id) u LEFT JOIN portal_deposit_scan_requests q USING(user_id)`, userID).
		Scan(&v.ServerTime, &v.State, &v.Message, &v.RequestedAt, &v.StartedAt, &v.FinishedAt, &v.CreditedMicro, &v.DepositCount)
	if err == nil && v.RequestedAt != nil {
		next := v.RequestedAt.Add(depositScanCooldown)
		v.NextAllowedAt = &next
		v.RetryAfterSeconds = max(0, int(math.Ceil(next.Sub(v.ServerTime).Seconds())))
	}
	return v, err
}

// Reading status is local database I/O only; it never initiates a chain scan.
func (s *Service) scanStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.depositScanStatus(r.Context(), currentUser(r).ID)
	if err != nil {
		s.dbError(w, err)
		return
	}
	jsonReply(w, http.StatusOK, v)
}

func (s *Service) scanNow(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := decode(w, r, &input); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := s.Settings(r.Context(), false)
	if err != nil {
		s.dbError(w, err)
		return
	}
	if !cfg.Wallet.Enabled {
		fail(w, http.StatusBadRequest, "USDT 自助充值尚未开启")
		return
	}
	userID := currentUser(r).ID // Never accept a user ID or destination supplied by the caller.
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var address string
	// The existing address row also serializes the first request across processes.
	err = tx.QueryRow(r.Context(), `SELECT address FROM portal_wallet_addresses WHERE user_id=$1 FOR UPDATE`, userID).Scan(&address)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, http.StatusConflict, "请先生成本用户的专属收款地址")
		return
	}
	if err != nil {
		s.dbError(w, err)
		return
	}
	var now time.Time
	if err = tx.QueryRow(r.Context(), `SELECT clock_timestamp()`).Scan(&now); err != nil {
		s.dbError(w, err)
		return
	}
	var previous time.Time
	var state string
	err = tx.QueryRow(r.Context(), `SELECT state,requested_at FROM portal_deposit_scan_requests WHERE user_id=$1`, userID).Scan(&state, &previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.dbError(w, err)
		return
	}
	if err == nil {
		remaining := int(math.Ceil(previous.Add(depositScanCooldown).Sub(now).Seconds()))
		if remaining > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(remaining))
			fail(w, http.StatusTooManyRequests, fmt.Sprintf("每位用户每分钟可检查一次，请在 %d 秒后重试", remaining))
			return
		}
		if state == "queued" || state == "running" {
			fail(w, http.StatusConflict, "充值检查正在排队或执行，请等待结果")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO portal_deposit_scan_requests(user_id,state,requested_at,message)VALUES($1,'queued',$2,'已排队，正在等待检查充值')
		ON CONFLICT(user_id)DO UPDATE SET state='queued',requested_at=EXCLUDED.requested_at,started_at=NULL,finished_at=NULL,message=EXCLUDED.message,credited_micro=0,deposit_count=0`, userID, now)
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r.Context(), "wallet.scan-request", userID)
	select {
	case s.scanRequested <- struct{}{}:
	default:
	}
	v, err := s.depositScanStatus(r.Context(), userID)
	if err != nil {
		s.dbError(w, err)
		return
	}
	jsonReply(w, http.StatusAccepted, v)
}

// The queue is durable, but a timer does not create work. A pending request can
// resume after a restart; all credits remain protected by (tx_id,event_index).
func (s *Service) processDepositScans(ctx context.Context, wallet WalletConfig) error {
	return s.locked(ctx, 42831002, func() error {
		for processed := 0; processed < 20; processed++ {
			var userID string
			var requestedAt time.Time
			err := s.db.QueryRow(ctx, `SELECT user_id,requested_at FROM portal_deposit_scan_requests WHERE state IN('queued','running') ORDER BY requested_at,user_id LIMIT 1`).Scan(&userID, &requestedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if _, err = s.db.Exec(ctx, `UPDATE portal_deposit_scan_requests SET state='running',started_at=coalesce(started_at,clock_timestamp()),message='正在查询链上固化交易' WHERE user_id=$1 AND requested_at=$2`, userID, requestedAt); err != nil {
				return err
			}
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			scanErr := s.scanWalletAddress(checkCtx, wallet, userID)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err() // Retain this explicit request for recovery on restart.
			}
			state, message := "succeeded", "暂未发现新的已确认充值，转账确认后会在后续检查中入账"
			if scanErr != nil {
				state, message = "failed", "本次检查暂未完成，保持窗口打开将自动重试；已核实的充值仍会正常入账"
				slog.Warn("requested deposit check failed", "user_id", userID, "error", scanErr)
			}
			var amount int64
			var count int
			if err = s.db.QueryRow(ctx, `SELECT coalesce(sum(amount_micro),0),count(*) FROM portal_deposits WHERE user_id=$1 AND created_at>=$2`, userID, requestedAt).Scan(&amount, &count); err != nil {
				return err
			}
			if scanErr == nil && count > 0 {
				message = fmt.Sprintf("检查完成，新增 %d 笔充值，余额与流水已更新", count)
			}
			if _, err = s.db.Exec(ctx, `UPDATE portal_deposit_scan_requests SET state=$3,finished_at=clock_timestamp(),message=$4,credited_micro=$5,deposit_count=$6 WHERE user_id=$1 AND requested_at=$2`, userID, requestedAt, state, message, amount, count); err != nil {
				return err
			}
		}
		return nil
	})
}
