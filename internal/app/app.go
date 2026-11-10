// Package app ensambla la aplicación: conecta los adaptadores con los casos de uso
// y los casos de uso con HTTP. Es el único lugar que conoce todas las piezas.
package app

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/adapter/httpapi"
	"github.com/lea/mediplan-api/internal/adapter/postgres"
	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/platform/push"
	"github.com/lea/mediplan-api/internal/platform/token"
	"github.com/lea/mediplan-api/internal/usecase/auth"
	"github.com/lea/mediplan-api/internal/usecase/caregivers"
	"github.com/lea/mediplan-api/internal/usecase/doses"
	"github.com/lea/mediplan-api/internal/usecase/notifications"
	"github.com/lea/mediplan-api/internal/usecase/plans"
	"github.com/lea/mediplan-api/internal/usecase/reminders"
	"github.com/lea/mediplan-api/internal/usecase/stock"
)

// Deps son las dependencias externas de la aplicación.
type Deps struct {
	Pool     *pgxpool.Pool
	Verifier auth.TokenVerifier   // Firebase en producción, un falso en los tests
	Pusher   notifications.Pusher // FCM; si es nil se usa uno que solo escribe en el log
	// JobSecret habilita las tareas programadas (POST /internal/jobs/*). Vacío = deshabilitadas.
	JobSecret string
	// AllowedOrigins: sitios web autorizados por CORS. Vacío = apagado.
	AllowedOrigins []string
}

func NewHandler(d Deps) http.Handler {
	if d.Pusher == nil {
		d.Pusher = push.LogPusher{}
	}
	users := postgres.NewUserRepo(d.Pool)
	planRepo := postgres.NewPlanRepo(d.Pool)
	links := postgres.NewInviteRepo(d.Pool) // implementa InviteRepo y CaregiverLinks
	doseRepo := postgres.NewDoseRepo(d.Pool)

	notifSvc := notifications.NewService(postgres.NewDeviceRepo(d.Pool), postgres.NewNotificationRepo(d.Pool), d.Pusher)

	plansSvc := plans.NewService(planRepo, links)
	plansSvc.SetSyncer(planSyncer{notifSvc}) // los cambios de horarios llegan a los celulares para reprogramar alarmas

	stockSvc := stock.NewService(postgres.NewStockRepo(d.Pool), plansSvc, participants{links}, notifSvc)
	dosesSvc := doses.NewService(plansSvc, users, doseRepo) // plansSvc decide quién ve el plan
	dosesSvc.SetStock(stockSvc)                             // cada toma descuenta una unidad

	h := httpapi.Handlers{
		AllowedOrigins: d.AllowedOrigins,
		Auth:           httpapi.NewAuthHandler(auth.NewService(d.Verifier, users)),
		Plans:          httpapi.NewPlansHandler(plansSvc),
		Caregivers:     httpapi.NewCaregiversHandler(caregivers.NewService(links, planRepo, token.Generator{})),
		Doses:          httpapi.NewDosesHandler(dosesSvc),
		Stock:          httpapi.NewStockHandler(stockSvc),
		Notifications:  httpapi.NewNotificationsHandler(notifSvc),
	}
	if d.JobSecret != "" {
		h.Jobs = httpapi.NewJobsHandler(d.JobSecret, reminders.NewService(postgres.NewReminderRepo(d.Pool), doseRepo, notifSvc))
	}
	return httpapi.NewRouter(h)
}

// planSyncer adapta notifications.Service a plans.Syncer: un push silencioso por participante.
type planSyncer struct{ svc *notifications.Service }

func (s planSyncer) Sync(ctx context.Context, userIDs []string, planID string) {
	for _, id := range userIDs {
		_ = s.svc.Sync(ctx, id, map[string]string{"planId": planID})
	}
}

// participants adapta los vínculos de Postgres a stock.Participants: el dueño y sus cuidadores aceptados.
type participants struct{ links *postgres.InviteRepo }

func (p participants) ParticipantIDs(ctx context.Context, plan *domain.Plan) ([]string, error) {
	ids, err := p.links.AcceptedCaregiverIDs(ctx, plan.ID)
	if err != nil {
		return nil, err
	}
	return append([]string{plan.PatientID}, ids...), nil
}
