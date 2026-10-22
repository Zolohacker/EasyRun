package store

import (
	"context"
	"errors"
	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/platform"
)

// TenantStore applies ownership to every request-facing resource access. Internal
// scheduler contexts have no Actor and retain system access. HTTP middleware is
// responsible for attaching an authenticated Actor before calling any handler.
type TenantStore struct{ Store }

// List queries apply this filter before ORDER/LIMIT, so other tenants cannot
// crowd a customer's own older records out of their result window.
func ownerFilter(ctx context.Context) string {
	a, ok := platform.ActorFrom(ctx)
	if ok && !a.IsAdmin() {
		if a.ID == "" {
			return "!invalid-owner"
		}
		return a.ID
	}
	return ""
}

func scoped(ctx context.Context) bool { a, ok := platform.ActorFrom(ctx); return ok && !a.IsAdmin() }
func (s TenantStore) GetAccount(ctx context.Context, id string) (platform.Account, error) {
	a, err := s.Store.GetAccount(ctx, id)
	if err != nil {
		return a, err
	}
	if actor, ok := platform.ActorFrom(ctx); ok && !actor.IsAdmin() && a.OwnerUserID != actor.ID {
		return platform.Account{}, ErrNotFound
	}
	return a, nil
}
func (s TenantStore) ListAccounts(ctx context.Context) ([]platform.Account, error) {
	items, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := []platform.Account{}
	actor, ok := platform.ActorFrom(ctx)
	for _, a := range items {
		if !ok || actor.IsAdmin() || a.OwnerUserID == actor.ID {
			out = append(out, a)
		}
	}
	return out, nil
}
func (s TenantStore) CreateAccount(ctx context.Context, a platform.Account) (platform.Account, error) {
	if actor, ok := platform.ActorFrom(ctx); ok {
		a.OwnerUserID = actor.ID
	}
	return s.Store.CreateAccount(ctx, a)
}
func (s TenantStore) UpdateAccount(ctx context.Context, a platform.Account) (platform.Account, error) {
	old, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		return platform.Account{}, err
	}
	a.OwnerUserID = old.OwnerUserID
	if a.DefaultPlanID != nil {
		if _, err = s.GetPlan(ctx, *a.DefaultPlanID); err != nil {
			return platform.Account{}, err
		}
	}
	if a.DefaultAreaID != nil {
		if _, err = s.GetArea(ctx, *a.DefaultAreaID); err != nil {
			return platform.Account{}, err
		}
	}
	result, err := s.Store.UpdateAccount(ctx, a)
	result.OwnerUserID = old.OwnerUserID
	return result, err
}
func (s TenantStore) DeleteAccount(ctx context.Context, id string) error {
	if _, err := s.GetAccount(ctx, id); err != nil {
		return err
	}
	return s.Store.DeleteAccount(ctx, id)
}
func (s TenantStore) allowAccount(ctx context.Context, id string) bool {
	if !scoped(ctx) {
		return true
	}
	if id == "" {
		return false
	}
	_, err := s.GetAccount(ctx, id)
	return err == nil
}
func (s TenantStore) GetPlan(ctx context.Context, id string) (platform.RunningPlan, error) {
	p, e := s.Store.GetPlan(ctx, id)
	if e == nil && !s.allowAccount(ctx, p.SourceAccountID) {
		return platform.RunningPlan{}, ErrNotFound
	}
	return p, e
}
func (s TenantStore) ListPlans(ctx context.Context) ([]platform.RunningPlan, error) {
	items, e := s.Store.ListPlans(ctx)
	out := []platform.RunningPlan{}
	for _, p := range items {
		if s.allowAccount(ctx, p.SourceAccountID) {
			out = append(out, p)
		}
	}
	return out, e
}
func (s TenantStore) CreatePlan(ctx context.Context, p platform.RunningPlan) (platform.RunningPlan, error) {
	if !s.allowAccount(ctx, p.SourceAccountID) {
		return platform.RunningPlan{}, ErrNotFound
	}
	return s.Store.CreatePlan(ctx, p)
}
func (s TenantStore) UpdatePlan(ctx context.Context, p platform.RunningPlan) (platform.RunningPlan, error) {
	if _, e := s.GetPlan(ctx, p.ID); e != nil {
		return platform.RunningPlan{}, e
	}
	if !s.allowAccount(ctx, p.SourceAccountID) {
		return platform.RunningPlan{}, ErrNotFound
	}
	return s.Store.UpdatePlan(ctx, p)
}
func (s TenantStore) DeletePlan(ctx context.Context, id string) error {
	if _, e := s.GetPlan(ctx, id); e != nil {
		return e
	}
	return s.Store.DeletePlan(ctx, id)
}
func (s TenantStore) GetArea(ctx context.Context, id string) (platform.RunningArea, error) {
	p, e := s.Store.GetArea(ctx, id)
	if e == nil && !s.allowAccount(ctx, p.SourceAccountID) {
		return platform.RunningArea{}, ErrNotFound
	}
	return p, e
}
func (s TenantStore) ListAreas(ctx context.Context) ([]platform.RunningArea, error) {
	items, e := s.Store.ListAreas(ctx)
	out := []platform.RunningArea{}
	for _, p := range items {
		if s.allowAccount(ctx, p.SourceAccountID) {
			out = append(out, p)
		}
	}
	return out, e
}
func (s TenantStore) CreateArea(ctx context.Context, p platform.RunningArea) (platform.RunningArea, error) {
	if !s.allowAccount(ctx, p.SourceAccountID) {
		return platform.RunningArea{}, ErrNotFound
	}
	return s.Store.CreateArea(ctx, p)
}
func (s TenantStore) UpdateArea(ctx context.Context, p platform.RunningArea) (platform.RunningArea, error) {
	if _, e := s.GetArea(ctx, p.ID); e != nil {
		return platform.RunningArea{}, e
	}
	if !s.allowAccount(ctx, p.SourceAccountID) {
		return platform.RunningArea{}, ErrNotFound
	}
	return s.Store.UpdateArea(ctx, p)
}
func (s TenantStore) DeleteArea(ctx context.Context, id string) error {
	if _, e := s.GetArea(ctx, id); e != nil {
		return e
	}
	return s.Store.DeleteArea(ctx, id)
}
func (s TenantStore) GetJob(ctx context.Context, id string) (platform.AutomationJob, error) {
	j, e := s.Store.GetJob(ctx, id)
	if e == nil && !s.allowAccount(ctx, j.AccountID) {
		return platform.AutomationJob{}, ErrNotFound
	}
	return j, e
}
func (s TenantStore) ListJobs(ctx context.Context) ([]platform.AutomationJob, error) {
	items, e := s.Store.ListJobs(ctx)
	out := []platform.AutomationJob{}
	for _, j := range items {
		if s.allowAccount(ctx, j.AccountID) {
			out = append(out, j)
		}
	}
	return out, e
}
func (s TenantStore) checkJob(ctx context.Context, j platform.AutomationJob) error {
	if _, e := s.GetAccount(ctx, j.AccountID); e != nil {
		return e
	}
	if _, e := s.GetPlan(ctx, j.PlanID); e != nil {
		return e
	}
	_, e := s.GetArea(ctx, j.AreaID)
	return e
}
func (s TenantStore) CreateJob(ctx context.Context, j platform.AutomationJob) (platform.AutomationJob, error) {
	if e := s.checkJob(ctx, j); e != nil {
		return platform.AutomationJob{}, e
	}
	return s.Store.CreateJob(ctx, j)
}
func (s TenantStore) UpdateJob(ctx context.Context, j platform.AutomationJob) (platform.AutomationJob, error) {
	if _, e := s.GetJob(ctx, j.ID); e != nil {
		return platform.AutomationJob{}, e
	}
	if e := s.checkJob(ctx, j); e != nil {
		return platform.AutomationJob{}, e
	}
	return s.Store.UpdateJob(ctx, j)
}
func (s TenantStore) DeleteJob(ctx context.Context, id string) error {
	if _, e := s.GetJob(ctx, id); e != nil {
		return e
	}
	return s.Store.DeleteJob(ctx, id)
}
func (s TenantStore) GetJobContext(ctx context.Context, id string) (platform.JobContext, error) {
	if _, e := s.GetJob(ctx, id); e != nil {
		return platform.JobContext{}, e
	}
	return s.Store.GetJobContext(ctx, id)
}
func (s TenantStore) ListExecutions(ctx context.Context, limit int) ([]platform.Execution, error) {
	items, e := s.Store.ListExecutions(ctx, limit)
	out := []platform.Execution{}
	for _, x := range items {
		if s.allowAccount(ctx, x.AccountID) {
			out = append(out, x)
		}
	}
	return out, e
}
func (s TenantStore) ListExecutionPage(ctx context.Context, q platform.ExecutionQuery) (platform.ExecutionPage, error) {
	pager, ok := s.Store.(platform.ExecutionPager)
	if !ok {
		return platform.ExecutionPage{}, errors.New("execution pagination is not supported by this store")
	}
	// PostgreSQL applies the actor filter before count, ordering and pagination.
	return pager.ListExecutionPage(ctx, q)
}
func (s TenantStore) Save(ctx context.Context, run activity.Run) error {
	if !s.allowAccount(ctx, run.AccountID) {
		return ErrNotFound
	}
	return s.Store.Save(ctx, run)
}
func (s TenantStore) Get(ctx context.Context, id string) (activity.Run, error) {
	r, e := s.Store.Get(ctx, id)
	if e == nil && !s.allowAccount(ctx, r.AccountID) {
		return activity.Run{}, ErrNotFound
	}
	return r, e
}
func (s TenantStore) List(ctx context.Context, limit int) ([]activity.ListItem, error) {
	items, e := s.Store.List(ctx, limit)
	if !scoped(ctx) {
		return items, e
	}
	out := []activity.ListItem{}
	for _, i := range items {
		if _, err := s.Get(ctx, i.ID); err == nil {
			out = append(out, i)
		}
	}
	return out, e
}

var _ Store = TenantStore{}
