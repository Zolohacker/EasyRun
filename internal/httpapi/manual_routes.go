package httpapi

import (
	"errors"
	"net/http"

	"github.com/Zolohacker/EasyRun/internal/platform"
	"github.com/Zolohacker/EasyRun/internal/store"
)

func (a *api) listManualRoutes(w http.ResponseWriter, r *http.Request) {
	items, e := a.store.ListManualRoutes(r.Context())
	if e != nil {
		a.storeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
}
func (a *api) saveManualRoute(w http.ResponseWriter, r *http.Request) {
	var m platform.ManualRoute
	if e := decodeJSON(w, r, &m, 1<<20); e != nil {
		writeError(w, 400, "invalid_route", e.Error())
		return
	}
	if _, e := a.store.GetAccount(r.Context(), m.AccountID); e != nil {
		a.storeError(w, e)
		return
	}
	p, e := a.store.GetPlan(r.Context(), m.PlanID)
	if e != nil {
		a.storeError(w, e)
		return
	}
	ar, e := a.store.GetArea(r.Context(), m.AreaID)
	if e != nil {
		a.storeError(w, e)
		return
	}
	if e = platform.ValidateManualRoute(&m, p, ar); e != nil {
		writeError(w, 400, "invalid_route", e.Error())
		return
	}
	status := http.StatusCreated
	if r.Method == "PUT" {
		m.ID = r.PathValue("id")
		m, e = a.store.UpdateManualRoute(r.Context(), m)
		status = 200
	} else {
		m.ID = ""
		m, e = a.store.CreateManualRoute(r.Context(), m)
	}
	if errors.Is(e, store.ErrNotFound) {
		a.storeError(w, e)
		return
	}
	if e != nil {
		writeError(w, 400, "invalid_route", e.Error())
		return
	}
	writeJSON(w, status, m)
}
func (a *api) deleteManualRoute(w http.ResponseWriter, r *http.Request) {
	e := a.store.DeleteManualRoute(r.Context(), r.PathValue("id"))
	if errors.Is(e, store.ErrNotFound) {
		a.storeError(w, e)
		return
	}
	if e != nil {
		writeError(w, 409, "route_in_use", e.Error())
		return
	}
	w.WriteHeader(204)
}
