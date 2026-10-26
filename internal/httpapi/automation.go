package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	automationpkg "github.com/Zolohacker/EasyRun/internal/automation"
	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/safety"
)

func (a *api) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListJobs(r.Context())
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) createJob(w http.ResponseWriter, r *http.Request) {
	var job platform.AutomationJob
	if err := decodeJSON(w, r, &job, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	job.ID = platform.NewID("job")
	if err := a.normalizeJob(r, &job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_job", err.Error())
		return
	}
	created, err := a.store.CreateJob(r.Context(), job)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *api) getJob(w http.ResponseWriter, r *http.Request) {
	item, err := a.store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *api) updateJob(w http.ResponseWriter, r *http.Request) {
	var job platform.AutomationJob
	if err := decodeJSON(w, r, &job, 1<<20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	job.ID = r.PathValue("id")
	if err := a.normalizeJob(r, &job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_job", err.Error())
		return
	}
	updated, err := a.store.UpdateJob(r.Context(), job)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *api) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteJob(r.Context(), r.PathValue("id")); err != nil {
		a.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) runJobNow(w http.ResponseWriter, r *http.Request) {
	if a.automation == nil {
		writeError(w, http.StatusServiceUnavailable, "automation_unavailable", "automation scheduler is not running")
		return
	}
	executionID, err := a.automation.Trigger(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, safety.ErrBlocked) || errors.Is(err, safety.ErrMaintenance) {
			writeError(w, http.StatusServiceUnavailable, "automation_blocked", err.Error())
			return
		}
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"execution_id": executionID, "status": "queued"})
}

func (a *api) listExecutions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("page") {
		pager, ok := a.store.(platform.ExecutionPager)
		if !ok {
			writeError(w, http.StatusNotImplemented, "pagination_unavailable", "当前存储不支持执行日志分页")
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		result, err := pager.ListExecutionPage(r.Context(), platform.ExecutionQuery{Page: page, PageSize: size, AccountID: r.URL.Query().Get("account_id"), Status: r.URL.Query().Get("status")})
		if err != nil {
			a.storeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := a.store.ListExecutions(r.Context(), limit)
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (a *api) normalizeJob(r *http.Request, job *platform.AutomationJob) error {
	return automationpkg.NormalizeJob(r.Context(), a.store, job)
}
