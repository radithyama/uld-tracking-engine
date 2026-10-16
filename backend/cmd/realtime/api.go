package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/radithya/uld-engine/internal/authz"
	"github.com/radithya/uld-engine/internal/httpx"
	"github.com/radithya/uld-engine/internal/loadopt"
	"github.com/radithya/uld-engine/internal/model"
	"github.com/radithya/uld-engine/internal/store"
)

// api serves the role-gated endpoints the Next.js backend-for-frontend calls
// on behalf of signed-in users. Every handler runs behind authz.Require, so
// the caller's identity and role come from a verified token.
type api struct {
	log   *slog.Logger
	pg    *store.Postgres
	cache *store.Redis
}

func (a *api) routes(secret []byte) http.Handler {
	r := chi.NewRouter()
	anyRole := authz.Require(secret, authz.Roles...)
	planners := authz.Require(secret, authz.RoleLoadmaster, authz.RoleSecurityAuditor)
	loadmaster := authz.Require(secret, authz.RoleLoadmaster)
	auditor := authz.Require(secret, authz.RoleSecurityAuditor)

	r.With(planners).Get("/flights/{id}/loadplan", a.getLoadPlan)
	r.With(loadmaster).Post("/flights/{id}/optimize", a.optimize)
	r.With(loadmaster).Post("/flights/{id}/loadplan/move", a.move)
	r.With(auditor).Get("/security/alerts", a.listAlerts)
	r.With(auditor).Post("/security/alerts/{id}/{action:ack|resolve}", a.transitionAlert)
	r.With(auditor).Get("/audit", a.listAudit)
	r.With(anyRole).Post("/audit", a.recordAudit)
	return r
}

func flightID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func (a *api) planContext(w http.ResponseWriter, r *http.Request) (store.FlightPlanContext, bool) {
	id, ok := flightID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, "invalid flight id", nil)
		return store.FlightPlanContext{}, false
	}
	pc, err := a.pg.FlightPlanContext(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "flight not found", nil)
		return pc, false
	case err != nil:
		a.log.Error("load plan context", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return pc, false
	}
	return pc, true
}

type loadPlanView struct {
	Flight    store.FlightPlanContext `json:"flight"`
	Plan      loadopt.Plan            `json:"plan"`
	Plannable bool                    `json:"plannable"`
}

func (a *api) getLoadPlan(w http.ResponseWriter, r *http.Request) {
	pc, ok := a.planContext(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, loadPlanView{
		Flight: pc, Plannable: pc.Plannable(),
		Plan: loadopt.Evaluate(pc.Aircraft, pc.Positions, pc.ULDs, pc.Current),
	})
}

func (a *api) optimize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Apply bool `json:"apply"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	pc, ok := a.planContext(w, r)
	if !ok {
		return
	}
	if !pc.Plannable() {
		httpx.WriteError(w, http.StatusConflict, store.ErrNotPlannable.Error(), nil)
		return
	}
	before := loadopt.Evaluate(pc.Aircraft, pc.Positions, pc.ULDs, pc.Current)
	proposed := loadopt.Optimize(pc.Aircraft, pc.Positions, pc.ReadyULDs())
	if req.Apply {
		c, _ := authz.FromContext(r.Context())
		if err := a.pg.SaveLoadPlan(r.Context(), pc.FlightID, proposed.Assignments, c.Name); err != nil {
			a.saveError(w, err)
			return
		}
		a.audit(r, "loadplan.optimize_applied", pc.FlightNumber, map[string]any{
			"flight_id": pc.FlightID, "cg_before": before.Balance.CGPctMAC, "cg_after": proposed.Balance.CGPctMAC,
			"assigned": len(proposed.Assignments), "unassigned": len(proposed.Unassigned),
		})
		_ = a.cache.PublishEvent(r.Context(), model.MsgLoadPlan, map[string]any{"flight_id": pc.FlightID, "by": c.Name})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"before": before, "proposed": proposed, "applied": req.Apply})
}

// move places one ULD at a position (or removes it from the plan when
// position is empty), rejecting anything that violates a hard limit.
func (a *api) move(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ULDID    string `json:"uld_id"`
		Position string `json:"position"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.ULDID == "" {
		httpx.WriteError(w, http.StatusBadRequest, "uld_id required", nil)
		return
	}
	pc, ok := a.planContext(w, r)
	if !ok {
		return
	}
	if !pc.Plannable() {
		httpx.WriteError(w, http.StatusConflict, store.ErrNotPlannable.Error(), nil)
		return
	}
	var target *loadopt.ULD
	for i := range pc.ULDs {
		if pc.ULDs[i].ID == req.ULDID {
			target = &pc.ULDs[i]
		}
	}
	switch {
	case target == nil:
		httpx.WriteError(w, http.StatusNotFound, "ULD is not booked on this flight", nil)
		return
	case target.Locked:
		httpx.WriteError(w, http.StatusConflict, "ULD is loaded; offload it before re-planning", nil)
		return
	case req.Position != "" && target.Status != "STAGED" && target.Status != "LOADED":
		httpx.WriteError(w, http.StatusConflict, "only STAGED or LOADED ULDs can be planned", nil)
		return
	}
	next := []loadopt.Assignment{}
	for _, as := range pc.Current {
		if as.ULDID != req.ULDID {
			next = append(next, as)
		}
	}
	if req.Position != "" {
		next = append(next, loadopt.Assignment{ULDID: req.ULDID, Position: req.Position})
	}
	plan := loadopt.Evaluate(pc.Aircraft, pc.Positions, pc.ULDs, next)
	for _, is := range plan.Issues {
		if is.ULDID == req.ULDID {
			httpx.WriteError(w, http.StatusUnprocessableEntity, is.Message, plan.Issues)
			return
		}
	}
	c, _ := authz.FromContext(r.Context())
	if err := a.pg.SaveLoadPlan(r.Context(), pc.FlightID, plan.Assignments, c.Name); err != nil {
		a.saveError(w, err)
		return
	}
	a.audit(r, "loadplan.manual_move", pc.FlightNumber, map[string]any{
		"flight_id": pc.FlightID, "uld_id": req.ULDID, "position": req.Position, "cg_after": plan.Balance.CGPctMAC,
	})
	_ = a.cache.PublishEvent(r.Context(), model.MsgLoadPlan, map[string]any{"flight_id": pc.FlightID, "by": c.Name})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plan": plan})
}

func (a *api) saveError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotPlannable) {
		httpx.WriteError(w, http.StatusConflict, err.Error(), nil)
		return
	}
	a.log.Error("save load plan", "err", err)
	httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
}

func (a *api) listAlerts(w http.ResponseWriter, r *http.Request) {
	status := strings.ToUpper(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	alerts, err := a.pg.ListSecurityAlerts(r.Context(), status, limit)
	if err != nil {
		a.log.Error("list alerts", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func (a *api) transitionAlert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req)
	if _, err := uuid.Parse(chi.URLParam(r, "id")); err != nil {
		httpx.WriteError(w, http.StatusNotFound, "alert not found", nil)
		return
	}
	to := map[string]string{"ack": "ACKNOWLEDGED", "resolve": "RESOLVED"}[chi.URLParam(r, "action")]
	c, _ := authz.FromContext(r.Context())
	alert, err := a.pg.TransitionAlert(r.Context(), chi.URLParam(r, "id"), to, c.Name, req.Note)
	switch {
	case errors.Is(err, store.ErrInvalidTransition):
		httpx.WriteError(w, http.StatusConflict, "alert not found or already in that state", nil)
		return
	case err != nil:
		a.log.Error("transition alert", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	a.audit(r, "security.alert_"+strings.ToLower(to), alert.RuleID, map[string]any{"alert_id": alert.ID, "note": req.Note})
	_ = a.cache.PublishSecurity(r.Context(), model.MsgSecurityAlertUpdate, alert)
	httpx.WriteJSON(w, http.StatusOK, alert)
}

func (a *api) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := a.pg.ListAudit(r.Context(), limit)
	if err != nil {
		a.log.Error("list audit", "err", err)
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// recordAudit lets the BFF log privileged actions it performs itself (e.g.
// sandbox scans), attributed to the verified caller.
func (a *api) recordAudit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action  string         `json:"action"`
		Target  string         `json:"target"`
		Details map[string]any `json:"details"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil ||
		!strings.HasPrefix(req.Action, "sandbox.") || len(req.Action) > 64 || len(req.Target) > 128 {
		httpx.WriteError(w, http.StatusBadRequest, "action must be sandbox.* (max 64 chars)", nil)
		return
	}
	e := a.audit(r, req.Action, req.Target, req.Details)
	httpx.WriteJSON(w, http.StatusCreated, e)
}

func (a *api) audit(r *http.Request, action, target string, details any) store.AuditEntry {
	c, _ := authz.FromContext(r.Context())
	e, err := a.pg.InsertAudit(r.Context(), c.Name+" <"+c.Sub+">", c.Role, action, target, details)
	if err != nil {
		a.log.Error("audit insert failed", "err", err, "action", action)
	}
	return e
}
