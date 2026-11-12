package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Handlers agrupa los manejadores de cada área. Los que están en nil no se publican
// (por ejemplo, en los tests de auth o cuando no hay secreto para las tareas programadas).
type Handlers struct {
	Auth          *AuthHandler
	Plans         *PlansHandler
	Caregivers    *CaregiversHandler
	Doses         *DosesHandler
	Notifications *NotificationsHandler
	Stock         *StockHandler
	Jobs          *JobsHandler
	// AllowedOrigins son los sitios web que pueden llamar a la API desde el navegador (CORS). Vacío = apagado.
	AllowedOrigins []string
}

func NewRouter(h Handlers) http.Handler {
	authH, plansH, cgH, dosesH, notifH := h.Auth, h.Plans, h.Caregivers, h.Doses, h.Notifications
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer, cors(h.AllowedOrigins))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	if h.Jobs != nil {
		// Lo llama Cloud Scheduler con un secreto compartido; no pasa por la sesión de usuario.
		r.Post("/internal/jobs/missed-doses", h.Jobs.MissedDoses)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/register", authH.Register) // solo requiere token de Firebase válido
		if cgH != nil {
			// Público: la página del enlace de invitación muestra el nombre del plan sin sesión.
			r.Get("/invitations/preview", cgH.Preview)
		}

		r.Group(func(r chi.Router) {
			r.Use(authH.Require)
			r.Get("/me", authH.Me)
			r.Patch("/me", authH.UpdateMe)
			r.Delete("/me", authH.DeleteMe)

			if plansH != nil {
				r.Route("/plans", func(r chi.Router) {
					r.Post("/", plansH.Create)
					r.Get("/", plansH.List)
					r.Get("/supervised", plansH.Supervised)
					r.Route("/{planID}", func(r chi.Router) {
						r.Get("/", plansH.Get)
						r.Patch("/", plansH.Rename)
						r.Delete("/", plansH.Delete)
						r.Post("/medications", plansH.AddMedication)
						r.Put("/medications/{medID}", plansH.UpdateMedication)  // reemplaza el medicamento completo
						r.Patch("/medications/{medID}", plansH.PatchMedication) // cambia solo lo que se manda (recomendado)
						r.Delete("/medications/{medID}", plansH.RemoveMedication)
						if h.Stock != nil {
							r.Post("/medications/{medID}/stock", h.Stock.Refill)
						}
						if dosesH != nil {
							r.Post("/medications/{medID}/doses", dosesH.RecordTaken)
							r.Get("/doses", dosesH.History)
							r.Get("/stats", dosesH.Stats)
						}
						if cgH != nil {
							r.Post("/caregivers", cgH.Invite)
							r.Get("/caregivers", cgH.List)
							r.Delete("/caregivers/{linkID}", cgH.Revoke)
							r.Delete("/follow", cgH.Leave)
						}
					})
				})
			}
			if cgH != nil {
				r.Route("/invitations", func(r chi.Router) {
					r.Get("/", cgH.Pending)
					r.Post("/accept", cgH.AcceptByToken)
					r.Post("/{id}/accept", cgH.AcceptByID)
					r.Post("/{id}/reject", cgH.Reject)
				})
			}
			if notifH != nil {
				r.Post("/devices", notifH.RegisterDevice)
				r.Delete("/devices", notifH.UnregisterDevice)
				r.Get("/notifications", notifH.List)
				r.Post("/notifications/{id}/read", notifH.MarkRead)
			}
		})
	})
	return r
}
