package httpapi

import (
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/activity"
	"github.com/Zolohacker/EasyRun/internal/deviceprofile"
	"github.com/Zolohacker/EasyRun/internal/geo"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
)

func (a *api) listPlans(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListPlans(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) createPlan(w http.ResponseWriter, r *http.Request) {
	var plan platform.RunningPlan
	if err := decodeJSON(w, r, &plan, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	plan.ID = ""
	if err := normalizePlan(&plan); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_plan", err.Error())
		return
	}
	created, err := a.store.CreatePlan(r.Context(), plan)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *api) getPlan(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *api) samplePlan(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Seed            int64   `json:"seed"`
		TargetDistanceM float64 `json:"target_distance_m"`
	}
	if err := decodeJSON(w, r, &request, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	plan, err := a.store.GetPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	if request.Seed == 0 {
		request.Seed = time.Now().UnixNano()
	}
	selection, err := activity.SampleTargetsForDistance(plan.TargetDistanceM, plan.Profile, plan.Rules, request.TargetDistanceM, request.Seed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "rule_sampling_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, selection)
}

func (a *api) updatePlan(w http.ResponseWriter, r *http.Request) {
	var plan platform.RunningPlan
	if err := decodeJSON(w, r, &plan, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	plan.ID = r.PathValue("id")
	if err := normalizePlan(&plan); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_plan", err.Error())
		return
	}
	updated, err := a.store.UpdatePlan(r.Context(), plan)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deletePlan(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeletePlan(r.Context(), r.PathValue("id")); err != nil {
		a.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listAreas(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListAreas(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) createArea(w http.ResponseWriter, r *http.Request) {
	var area platform.RunningArea
	if err := decodeJSON(w, r, &area, 2<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	area.ID = ""
	if err := normalizeArea(&area); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_area", err.Error())
		return
	}
	created, err := a.store.CreateArea(r.Context(), area)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *api) getArea(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetArea(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *api) updateArea(w http.ResponseWriter, r *http.Request) {
	var area platform.RunningArea
	if err := decodeJSON(w, r, &area, 2<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	area.ID = r.PathValue("id")
	if err := normalizeArea(&area); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_area", err.Error())
		return
	}
	updated, err := a.store.UpdateArea(r.Context(), area)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteArea(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteArea(r.Context(), r.PathValue("id")); err != nil {
		a.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListAccounts(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) createAccount(w http.ResponseWriter, r *http.Request) {
	a.onboardAccountPassword(w, r)
}

func (a *api) onboardAccountPassword(w http.ResponseWriter, r *http.Request) {
	var request accountRequest
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	account := request.Account
	account.ID = ""
	if err := a.validateAccount(r, &account); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_account", err.Error())
		return
	}
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	ctx, cancel := contextWithProtocolTimeout(r)
	defer cancel()
	result, err := a.runner.OnboardPassword(ctx, account, request.Password)
	if err != nil {
		a.protocolError(w, err)
		return
	}
	status := http.StatusCreated
	if result.VerificationRequired {
		status = http.StatusAccepted
	}
	writeJSON(w, status, result)
}

func (a *api) getAccount(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetAccount(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *api) updateAccount(w http.ResponseWriter, r *http.Request) {
	var request accountRequest
	if err := decodeJSON(w, r, &request, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	account := request.Account
	account.ID = r.PathValue("id")
	if err := a.validateAccount(r, &account); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_account", err.Error())
		return
	}
	if a.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "protocol_unavailable", "账号服务未启动")
		return
	}
	updated, err := a.runner.UpdateAccount(r.Context(), account, request.Password)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteAccount(r.Context(), r.PathValue("id")); err != nil {
		a.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func normalizePlan(plan *platform.RunningPlan) error {
	plan.Name = strings.TrimSpace(plan.Name)
	plan.Description = strings.TrimSpace(plan.Description)
	if plan.Name == "" || len([]rune(plan.Name)) > 80 {
		return errors.New("name is required and must not exceed 80 characters")
	}
	if plan.TargetDistanceM < 300 || plan.TargetDistanceM > 100000 {
		return errors.New("target_distance_m must be between 300 and 100000")
	}
	profile, err := activity.NormalizeProfile(plan.Profile)
	if err != nil {
		return err
	}
	plan.Profile = profile
	return nil
}

func normalizeArea(area *platform.RunningArea) error {
	area.Name = strings.TrimSpace(area.Name)
	area.Description = strings.TrimSpace(area.Description)
	if area.Name == "" || len([]rune(area.Name)) > 80 {
		return errors.New("name is required and must not exceed 80 characters")
	}
	if !area.Center.Valid() {
		return errors.New("center is invalid")
	}
	if len(area.Boundary) > 0 && len(area.Boundary) < 3 {
		return errors.New("boundary requires at least three points")
	}
	for _, point := range area.Boundary {
		if !point.Valid() {
			return errors.New("boundary contains an invalid coordinate")
		}
	}
	if len(area.Boundary) >= 3 && !containsPoint(area.Center.Lat, area.Center.Lng, area.Boundary) {
		return errors.New("center must be inside boundary")
	}
	if area.CandidateCount == 0 {
		area.CandidateCount = 4
	}
	if area.CandidateCount < 1 || area.CandidateCount > 8 {
		return errors.New("candidate_count must be between 1 and 8")
	}
	for area.HeadingDeg < 0 {
		area.HeadingDeg += 360
	}
	for area.HeadingDeg >= 360 {
		area.HeadingDeg -= 360
	}
	return nil
}

func containsPoint(lat, lng float64, polygon []geo.Point) bool {
	inside := false
	for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
		a, b := polygon[i], polygon[j]
		if (a.Lat > lat) != (b.Lat > lat) && lng < (b.Lng-a.Lng)*(lat-a.Lat)/(b.Lat-a.Lat)+a.Lng {
			inside = !inside
		}
	}
	return inside
}

func (a *api) validateAccount(r *http.Request, account *platform.Account) error {
	account.LoginName = strings.TrimSpace(account.LoginName)
	account.Name = strings.TrimSpace(account.Name)
	account.Note = strings.TrimSpace(account.Note)
	if account.LoginName == "" || len([]rune(account.LoginName)) > 100 {
		return errors.New("login_name is required and must not exceed 100 characters")
	}
	if account.Name == "" {
		account.Name = account.LoginName
	}
	if account.Name == "" || len([]rune(account.Name)) > 80 {
		return errors.New("name is required and must not exceed 80 characters")
	}
	if account.DefaultPlanID != nil {
		plan, err := a.store.GetPlan(r.Context(), *account.DefaultPlanID)
		if err != nil {
			return errors.New("default_plan_id does not exist")
		}
		if account.ID != "" && plan.SourceAccountID != "" && plan.SourceAccountID != account.ID {
			return errors.New("default_plan_id belongs to another account")
		}
	}
	if account.DefaultAreaID != nil {
		area, err := a.store.GetArea(r.Context(), *account.DefaultAreaID)
		if err != nil {
			return errors.New("default_area_id does not exist")
		}
		if account.ID != "" && area.SourceAccountID != "" && area.SourceAccountID != account.ID {
			return errors.New("default_area_id belongs to another account")
		}
	}
	if account.FenceIndex < 0 || account.FenceIndex > 100 {
		return errors.New("fence_index must be between 0 and 100")
	}
	account.DeviceID = strings.ToLower(strings.TrimSpace(account.DeviceID))
	if account.DeviceID != "" {
		if len(account.DeviceID) != 16 {
			return errors.New("device_id must contain exactly 16 hexadecimal characters")
		}
		if _, err := hex.DecodeString(account.DeviceID); err != nil {
			return errors.New("device_id must contain exactly 16 hexadecimal characters")
		}
	}
	account.ModelName = strings.TrimSpace(account.ModelName)
	account.SystemVersion = strings.TrimSpace(account.SystemVersion)
	if account.ID == "" {
		model, version, err := deviceprofile.Complete(account.ModelName, account.SystemVersion, time.Now())
		if err != nil {
			return err
		}
		account.ModelName, account.SystemVersion = model, version
	}
	return nil
}

type accountRequest struct {
	platform.Account
	Password string `json:"password,omitempty"`
}

func (a *api) storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "resource does not exist")
		return
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "foreign key") || strings.Contains(message, "violates") || strings.Contains(message, "duplicate") {
		writeError(w, http.StatusConflict, "constraint_conflict", err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "database_error", err.Error())
}
