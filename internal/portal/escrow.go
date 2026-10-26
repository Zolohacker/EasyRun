package portal

import (
	"context"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5"
)

// Integer proportional allocation preserves the original prepaid principal.
// The final unit receives all rounding remainder; catalogue price changes never
// increase a refund. Available and in-flight money are separate pools.
func escrowShare(amount, units, total int64) int64 {
	if units <= 0 || total <= 0 {
		return 0
	}
	if units >= total {
		return amount
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(units))
	return n.Quo(n, big.NewInt(total)).Int64()
}

const escrowSchema = `
ALTER TABLE portal_task_credits ADD COLUMN IF NOT EXISTS available_micro bigint NOT NULL DEFAULT 0 CHECK(available_micro>=0);
ALTER TABLE portal_task_credits ADD COLUMN IF NOT EXISTS settled_units bigint NOT NULL DEFAULT 0 CHECK(settled_units>=0);
ALTER TABLE portal_task_credits ADD COLUMN IF NOT EXISTS settled_micro bigint NOT NULL DEFAULT 0 CHECK(settled_micro>=0);
ALTER TABLE portal_task_credits ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 1;
ALTER TABLE portal_task_credits ADD COLUMN IF NOT EXISTS escrow_version integer NOT NULL DEFAULT 0;
ALTER TABLE portal_ledger ADD COLUMN IF NOT EXISTS funding_amount_micro bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS portal_task_adjustments(
 id text PRIMARY KEY,user_id text NOT NULL REFERENCES portal_users(id),job_id text NOT NULL,
 mode text NOT NULL,target_units bigint NOT NULL,delta_units bigint NOT NULL,delta_micro bigint NOT NULL,
 expected_revision bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS portal_task_orders(
 id text PRIMARY KEY,user_id text NOT NULL REFERENCES portal_users(id),job_id text NOT NULL,
 request_hash text NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
-- Existing purchases already reduced available wallet balance. Reclassify only
-- their unconsumed principal; do not credit wallets or refund successful runs.
WITH totals AS (
 SELECT c.job_id,COALESCE(sum(o.units),0)::numeric units,COALESCE(sum(o.amount_micro),0)::numeric amount
 FROM portal_task_credits c LEFT JOIN portal_task_credit_orders o ON o.job_id=c.job_id AND o.mode=c.mode
 WHERE c.escrow_version=0 GROUP BY c.job_id
)
UPDATE portal_charges ch SET amount_micro=CASE WHEN t.units>0 THEN floor(t.amount*ch.credit_units/t.units)::bigint ELSE 0 END
FROM totals t,portal_task_credits c WHERE ch.credit_job_id=t.job_id AND c.job_id=t.job_id AND ch.credit_mode=c.mode AND ch.funding_source='prepaid' AND ch.state IN('reserved','settled');
-- Give rounding residual to available principal; if there is none, to one
-- in-flight charge, or finally to historical settlement. Never strand a micro.
WITH totals AS (
 SELECT c.job_id,COALESCE(sum(o.amount_micro),0)::numeric amount
 FROM portal_task_credits c LEFT JOIN portal_task_credit_orders o ON o.job_id=c.job_id AND o.mode=c.mode
 WHERE c.escrow_version=0 AND c.remaining_units=0 GROUP BY c.job_id
), shares AS (
 SELECT c.job_id,COALESCE(sum(ch.amount_micro),0)::numeric amount,
 COALESCE(min(ch.execution_id) FILTER(WHERE ch.state='reserved'),min(ch.execution_id)) target
 FROM portal_task_credits c JOIN portal_charges ch ON ch.credit_job_id=c.job_id AND ch.credit_mode=c.mode AND ch.funding_source='prepaid' AND ch.state IN('reserved','settled')
 WHERE c.escrow_version=0 GROUP BY c.job_id
)
UPDATE portal_charges ch SET amount_micro=ch.amount_micro+(t.amount-sp.amount)::bigint
FROM totals t,shares sp WHERE t.job_id=sp.job_id AND ch.execution_id=sp.target;
WITH totals AS (
 SELECT c.job_id,COALESCE(sum(o.amount_micro),0)::numeric amount
 FROM portal_task_credits c LEFT JOIN portal_task_credit_orders o ON o.job_id=c.job_id AND o.mode=c.mode
 WHERE c.escrow_version=0 GROUP BY c.job_id
), spent AS (
 SELECT c.job_id,COALESCE(sum(ch.credit_units) FILTER(WHERE ch.state='settled'),0)::bigint units,
 COALESCE(sum(ch.amount_micro) FILTER(WHERE ch.state='settled'),0)::bigint settled,
 COALESCE(sum(ch.amount_micro) FILTER(WHERE ch.state='reserved'),0)::bigint held
 FROM portal_task_credits c LEFT JOIN portal_charges ch ON ch.credit_job_id=c.job_id AND ch.credit_mode=c.mode AND ch.funding_source='prepaid'
 WHERE c.escrow_version=0 GROUP BY c.job_id
)
UPDATE portal_task_credits c SET
 available_micro=(t.amount-sp.settled-sp.held)::bigint,
 settled_units=sp.units,settled_micro=sp.settled,escrow_version=1
 FROM totals t,spent sp WHERE c.job_id=t.job_id AND sp.job_id=t.job_id;
`

func settleTaskCredit(ctx context.Context, tx pgx.Tx, execution, job, user, mode string, units, amount int64, success bool) error {
	query := `UPDATE portal_task_credits SET remaining_units=remaining_units+$3,available_micro=available_micro+$4,revision=revision+1,updated_at=now() WHERE job_id=$1 AND user_id=$2 AND mode=$5`
	if success {
		query = `UPDATE portal_task_credits SET settled_units=settled_units+$3,settled_micro=settled_micro+$4,revision=revision+1,updated_at=now() WHERE job_id=$1 AND user_id=$2 AND mode=$5`
	}
	tag, e := tx.Exec(ctx, query, job, user, units, amount, mode)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return errors.New("预扣资金与任务绑定不一致，请核对账务")
	}
	if success {
		_, e = tx.Exec(ctx, `INSERT INTO portal_ledger(id,user_id,delta_micro,kind,reference,funding_amount_micro) VALUES($1,$2,0,'task_settle',$3,$4)`, "settle_"+execution, user, "settle:"+execution, amount)
	}
	return e
}
