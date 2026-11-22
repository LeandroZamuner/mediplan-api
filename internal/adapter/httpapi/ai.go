package httpapi

import (
	"context"
	"net/http"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/ai"
	"github.com/lea/mediplan-api/internal/usecase/assistant"
)

// PrefsStore guarda las preferencias de correos e IA.
type PrefsStore interface {
	Get(ctx context.Context, userID string) (domain.Preferences, error)
	Set(ctx context.Context, userID string, p domain.Preferences) error
}

// Tiers dice si la cuenta es Premium (el resumen semanal es solo Premium).
type Tiers interface {
	TierOf(ctx context.Context, userID string) (domain.Tier, error)
}

// AIHandler expone las preferencias (consentimiento) y el chat con el asistente.
type AIHandler struct {
	prefs PrefsStore
	tiers Tiers
	chat  *assistant.Service // nil = el asistente no está habilitado
}

func NewAIHandler(prefs PrefsStore, tiers Tiers, chat *assistant.Service) *AIHandler {
	return &AIHandler{prefs: prefs, tiers: tiers, chat: chat}
}

type prefsDTO struct {
	WeeklyReport bool `json:"weeklyReport"`
	AIConsent    bool `json:"aiConsent"`
}

// GetPreferences: GET /me/preferences
func (h *AIHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	p, err := h.prefs.Get(r.Context(), u.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"weeklyReport": p.WeeklyReport, "aiConsent": p.AIConsent, "assistantAvailable": h.chat != nil})
}

// SetPreferences: PUT /me/preferences {"weeklyReport":bool,"aiConsent":bool}. El resumen semanal requiere Premium.
func (h *AIHandler) SetPreferences(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	var body prefsDTO
	if !decode(w, r, &body) {
		return
	}
	if body.WeeklyReport {
		if t, err := h.tiers.TierOf(r.Context(), u.ID); err != nil {
			writeError(w, err)
			return
		} else if t != domain.TierPremium {
			writeError(w, domain.ErrPremiumRequired)
			return
		}
	}
	p := domain.Preferences{WeeklyReport: body.WeeklyReport, AIConsent: body.AIConsent}
	if err := h.prefs.Set(r.Context(), u.ID, p); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"weeklyReport": p.WeeklyReport, "aiConsent": p.AIConsent, "assistantAvailable": h.chat != nil})
}

// Chat: POST /assistant/chat {"messages":[{"role":"user|assistant","text":"..."}]} -> {"reply":"..."}
// El cliente reenvía la conversación completa: el servidor no la guarda.
func (h *AIHandler) Chat(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	var body struct {
		Messages []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if !decode(w, r, &body) {
		return
	}
	msgs := make([]ai.Msg, 0, len(body.Messages))
	for _, m := range body.Messages {
		msgs = append(msgs, ai.Msg{Role: m.Role, Text: m.Text})
	}
	reply, err := h.chat.Chat(r.Context(), u, msgs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}
