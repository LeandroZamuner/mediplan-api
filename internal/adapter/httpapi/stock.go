package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/lea/mediplan-api/internal/usecase/stock"
)

type StockHandler struct{ svc *stock.Service }

func NewStockHandler(svc *stock.Service) *StockHandler { return &StockHandler{svc: svc} }

// Refill: POST /plans/{planID}/medications/{medID}/stock {"units": 30}  (solo suma)
func (h *StockHandler) Refill(w http.ResponseWriter, r *http.Request) {
	actor, _ := UserFromContext(r.Context())
	var body struct {
		Units int `json:"units"`
	}
	if !decode(w, r, &body) {
		return
	}
	total, err := h.svc.Refill(r.Context(), actor, chi.URLParam(r, "planID"), chi.URLParam(r, "medID"), body.Units)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"medicationId": chi.URLParam(r, "medID"), "stockUnits": total})
}
