package safety

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zolohacker/EasyRun/internal/protocol"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBlocked = errors.New("跑步自动化已暂停")
var ErrMaintenance = errors.New("平台正在维护更新")

const CheckInterval = 5 * time.Minute
const maxCachedAge = 10 * time.Minute

type Checker interface {
	CheckAppUpdate(context.Context, protocol.Device) (protocol.AppUpdate, error)
}

type Status struct {
	CurrentVersion string     `json:"current_version"`
	CurrentBuild   string     `json:"current_build"`
	LatestVersion  string     `json:"latest_version"`
	SupportVersion string     `json:"support_version"`
	HaveUpdate     bool       `json:"have_update"`
	ForceUpdate    bool       `json:"force_update"`
	Known          bool       `json:"known"`
	Allowed        bool       `json:"automation_allowed"`
	Reason         string     `json:"reason"`
	LastError      string     `json:"last_error"`
	CheckedAt      *time.Time `json:"checked_at,omitempty"`
	SuccessAt      *time.Time `json:"success_at,omitempty"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Maintenance    bool       `json:"maintenance"`
}

type Guard struct {
	db        *pgxpool.Pool
	checker   Checker
	device    protocol.Device
	key       string
	mu        sync.Mutex
	refreshMu sync.Mutex
	status    Status
	active    map[uint64]context.CancelCauseFunc
	sequence  uint64
	pauseID   uint64
	pause     string
	clock     func() time.Time
}

func New(ctx context.Context, db *pgxpool.Pool, checker Checker, source string, device protocol.Device) (*Guard, error) {
	digest := sha256.Sum256([]byte(source + ":" + protocol.AppVersion + ":" + protocol.BuildVersion))
	g := &Guard{db: db, checker: checker, device: device, key: hex.EncodeToString(digest[:]), clock: time.Now, active: map[uint64]context.CancelCauseFunc{}}
	if db != nil {
		if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS client_version_checks(source_key text PRIMARY KEY,status jsonb NOT NULL)`); err != nil {
			return nil, err
		}
		var raw []byte
		err := db.QueryRow(ctx, `SELECT status FROM client_version_checks WHERE source_key=$1`, g.key).Scan(&raw)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal(raw, &g.status); err != nil {
				return nil, err
			}
		}
	}
	return g, nil
}

func (g *Guard) reasonLocked() error {
	if g.pause != "" {
		return fmt.Errorf("%w：%s", ErrMaintenance, g.pause)
	}
	if g.status.ForceUpdate {
		return fmt.Errorf("%w：闪动校园要求强制更新到 %s，当前适配 %s；请先更新平台适配", ErrBlocked, g.status.LatestVersion, protocol.AppVersion)
	}
	if !g.status.Known || g.status.SuccessAt == nil {
		return fmt.Errorf("%w：尚未成功核实客户端更新状态", ErrBlocked)
	}
	age := g.clock().Sub(*g.status.SuccessAt)
	if age > maxCachedAge || age < -time.Minute {
		return fmt.Errorf("%w：客户端更新状态已过期，请重新检查", ErrBlocked)
	}
	return nil
}

func (g *Guard) Snapshot() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	v := g.status
	v.CurrentVersion, v.CurrentBuild = protocol.AppVersion, protocol.BuildVersion
	v.Maintenance = g.pause != ""
	v.Allowed = g.reasonLocked() == nil
	if err := g.reasonLocked(); err != nil {
		v.Reason = err.Error()
	} else {
		v.Reason = "当前客户端无需强制更新，跑步自动化可执行"
	}
	return v
}

func (g *Guard) Refresh(ctx context.Context, force bool) error {
	g.refreshMu.Lock()
	defer g.refreshMu.Unlock()
	g.mu.Lock()
	now := g.clock()
	if !force && g.status.CheckedAt != nil && now.Sub(*g.status.CheckedAt) >= 0 && now.Sub(*g.status.CheckedAt) < CheckInterval {
		g.mu.Unlock()
		return nil
	}
	g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := g.checker.CheckAppUpdate(checkCtx, g.device)
	g.mu.Lock()
	g.status.CheckedAt = &now
	if err == nil {
		g.status.Known, g.status.HaveUpdate, g.status.ForceUpdate = true, result.HaveUpdate, result.ForceUpdate
		g.status.LatestVersion, g.status.SupportVersion = result.NewVersion, result.SupportVersion
		g.status.Title, g.status.Description = result.UpdateTitle, result.UpdateDescription
		g.status.SuccessAt, g.status.LastError = &now, ""
	} else {
		g.status.LastError = "检查客户端版本失败：" + err.Error()
		// Keep any previously observed forceUpdate=true on transport/format errors.
	}
	if blocked := g.reasonLocked(); blocked != nil && !errors.Is(blocked, ErrMaintenance) {
		for _, cancel := range g.active {
			cancel(blocked)
		}
	}
	raw, marshalErr := json.Marshal(g.status)
	g.mu.Unlock()
	if marshalErr != nil {
		return marshalErr
	}
	if g.db != nil {
		if _, saveErr := g.db.Exec(ctx, `INSERT INTO client_version_checks(source_key,status)VALUES($1,$2)ON CONFLICT(source_key)DO UPDATE SET status=EXCLUDED.status`, g.key, raw); saveErr != nil {
			return saveErr
		}
	}
	return err
}

func (g *Guard) Check(ctx context.Context) error {
	_ = g.Refresh(ctx, false)
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reasonLocked()
}

func (g *Guard) Begin(ctx context.Context) (context.Context, func(), error) {
	if err := g.Check(ctx); err != nil {
		return ctx, func() {}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.reasonLocked(); err != nil {
		return ctx, func() {}, err
	}
	child, cancel := context.WithCancelCause(ctx)
	g.sequence++
	id := g.sequence
	g.active[id] = cancel
	return child, func() {
		cancel(nil)
		g.mu.Lock()
		delete(g.active, id)
		g.mu.Unlock()
	}, nil
}

// Pause blocks new submissions atomically, and never terminates an active run.
func (g *Guard) Pause(reason string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pause != "" || len(g.active) > 0 {
		return nil, errors.New("仍有运行中任务或平台更新正在进行，请稍后重试")
	}
	g.pauseID++
	id := g.pauseID
	g.pause = reason
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.pauseID == id {
				g.pause = ""
			}
		})
	}, nil
}

func (g *Guard) Run(ctx context.Context) {
	_ = g.Refresh(ctx, false)
	ticker := time.NewTicker(CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = g.Refresh(ctx, true)
		}
	}
}
