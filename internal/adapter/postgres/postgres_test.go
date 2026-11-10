package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/adapter/postgres/postgrestest"
	"github.com/lea/mediplan-api/internal/domain"
)

func TestMain(m *testing.M) { os.Exit(postgrestest.Run(m)) }

var ctx = context.Background()

// ---- givens ----

type db struct {
	pool    *pgxpool.Pool
	users   *UserRepo
	plans   *PlanRepo
	invites *InviteRepo
	doses   *DoseRepo
	devices *DeviceRepo
	notifs  *NotificationRepo
	remind  *ReminderRepo
	stock   *StockRepo
}

func givenDB(t *testing.T) db {
	t.Helper()
	pool := postgrestest.Setup(t)
	return db{pool: pool, users: NewUserRepo(pool), plans: NewPlanRepo(pool), invites: NewInviteRepo(pool), doses: NewDoseRepo(pool), devices: NewDeviceRepo(pool), notifs: NewNotificationRepo(pool), remind: NewReminderRepo(pool), stock: NewStockRepo(pool)}
}

func (d db) givenUser(t *testing.T, name string) *domain.User {
	t.Helper()
	u := &domain.User{FirebaseUID: "fb-" + name, Email: name + "@mail.com", Role: domain.RolePatient, EmailVerified: true, CreatedAt: time.Now()}
	if err := d.users.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	return u
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func medication(name string) domain.Medication {
	end := day(2026, 12, 31)
	return domain.Medication{
		Name: name, Dose: "10 mg", StartDate: day(2026, 11, 1), EndDate: &end,
		Schedules:  []domain.Schedule{{DayOfWeek: 1, TimeOfDay: "08:00"}, {DayOfWeek: 4, TimeOfDay: "20:30"}},
		StockUnits: 30, StockAlertAt: 5,
	}
}

func (d db) givenPlan(t *testing.T, owner *domain.User, meds ...string) *domain.Plan {
	t.Helper()
	p := &domain.Plan{PatientID: owner.ID, Name: "Hipertensión", Status: domain.PlanActive, CreatedAt: time.Now()}
	for _, m := range meds {
		p.Medications = append(p.Medications, medication(m))
	}
	if err := d.plans.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (d db) givenInvite(t *testing.T, planID, email, tokenHash string) *domain.PlanCaregiver {
	t.Helper()
	c := &domain.PlanCaregiver{PlanID: planID, InviteEmail: email, Status: domain.InvitePending,
		TokenHash: tokenHash, ExpiresAt: time.Now().Add(7 * 24 * time.Hour), CreatedAt: time.Now()}
	if err := d.invites.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	return c
}

// ---- usuarios ----

func TestUserRepo(t *testing.T) {
	t.Run("dado un usuario nuevo, cuando se crea y se busca por uid de Firebase, entonces se recupera con su id", func(t *testing.T) {
		d := givenDB(t)
		u := d.givenUser(t, "ana")
		got, err := d.users.ByFirebaseUID(ctx, "fb-ana")
		if err != nil || u.ID == "" || got.ID != u.ID || got.Email != "ana@mail.com" || got.Role != domain.RolePatient || !got.EmailVerified {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("dado un usuario nuevo, cuando se le cambia la zona horaria, entonces tiene la de Argentina al crearse y la nueva después", func(t *testing.T) {
		d := givenDB(t)
		u := d.givenUser(t, "ana")
		antes, _ := d.users.ByFirebaseUID(ctx, "fb-ana")
		if antes.Timezone != domain.DefaultTimezone {
			t.Fatalf("por defecto: got %q", antes.Timezone)
		}
		if err := d.users.SetTimezone(ctx, u.ID, "America/Montevideo"); err != nil {
			t.Fatal(err)
		}
		despues, _ := d.users.ByFirebaseUID(ctx, "fb-ana")
		if despues.Timezone != "America/Montevideo" {
			t.Fatalf("después: got %q", despues.Timezone)
		}
		if err := d.users.SetTimezone(ctx, "00000000-0000-0000-0000-000000000000", "UTC"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("usuario inexistente: got %v", err)
		}
	})

	t.Run("dado un uid inexistente, cuando se busca, entonces no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		if _, err := d.users.ByFirebaseUID(ctx, "nadie"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
	})

	t.Run("dado un usuario existente, cuando se crea otro con el mismo uid o el mismo email, entonces ya existe", func(t *testing.T) {
		d := givenDB(t)
		d.givenUser(t, "ana")
		mismoUID := &domain.User{FirebaseUID: "fb-ana", Email: "otro@mail.com", Role: domain.RolePatient}
		mismoMail := &domain.User{FirebaseUID: "fb-otro", Email: "ana@mail.com", Role: domain.RolePatient}
		for _, u := range []*domain.User{mismoUID, mismoMail} {
			if err := d.users.Create(ctx, u); !errors.Is(err, domain.ErrAlreadyExists) {
				t.Errorf("%s: esperaba ErrAlreadyExists, got %v", u.Email, err)
			}
		}
	})
}

// ---- planes ----

func TestPlanRepoCreateAndRead(t *testing.T) {
	t.Run("dado un plan con dos medicamentos, cuando se crea y se lee, entonces vuelve completo y en el mismo orden", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p := d.givenPlan(t, ana, "Enalapril", "Aspirina")
		got, err := d.plans.ByID(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Hipertensión" || got.PatientID != ana.ID || got.Status != domain.PlanActive || len(got.Medications) != 2 {
			t.Fatalf("plan inesperado: %+v", got)
		}
		m := got.Medications[0]
		if m.ID == "" || m.Name != "Enalapril" || got.Medications[1].Name != "Aspirina" {
			t.Fatalf("orden o ids incorrectos: %+v", got.Medications)
		}
		if m.Dose != "10 mg" || !m.StartDate.Equal(day(2026, 11, 1)) || m.EndDate == nil || !m.EndDate.Equal(day(2026, 12, 31)) || m.StockUnits != 30 || m.StockAlertAt != 5 {
			t.Fatalf("datos del medicamento incorrectos: %+v", m)
		}
		if len(m.Schedules) != 2 || m.Schedules[0] != (domain.Schedule{DayOfWeek: 1, TimeOfDay: "08:00"}) || m.Schedules[1] != (domain.Schedule{DayOfWeek: 4, TimeOfDay: "20:30"}) {
			t.Fatalf("horarios incorrectos: %+v", m.Schedules)
		}
	})

	t.Run("dado un medicamento sin fecha de fin, cuando se lee, entonces EndDate es nil", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p := &domain.Plan{PatientID: ana.ID, Name: "P", Status: domain.PlanActive, CreatedAt: time.Now()}
		m := medication("X")
		m.EndDate = nil
		p.Medications = []domain.Medication{m}
		if err := d.plans.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		got, _ := d.plans.ByID(ctx, p.ID)
		if got.Medications[0].EndDate != nil {
			t.Fatalf("EndDate debería ser nil, got %v", got.Medications[0].EndDate)
		}
	})

	t.Run("dado un id inexistente o que no es un uuid, cuando se busca el plan, entonces no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
			if _, err := d.plans.ByID(ctx, id); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%q: esperaba ErrNotFound, got %v", id, err)
			}
		}
	})

	t.Run("dados planes de Ana y de Lucía, cuando se listan los de Ana, entonces solo vienen los suyos", func(t *testing.T) {
		d := givenDB(t)
		ana, lucia := d.givenUser(t, "ana"), d.givenUser(t, "lucia")
		d.givenPlan(t, ana, "A")
		d.givenPlan(t, ana, "B")
		d.givenPlan(t, lucia, "C")
		got, err := d.plans.ByPatient(ctx, ana.ID)
		if err != nil || len(got) != 2 || len(got[0].Medications) != 1 {
			t.Fatalf("got %d err=%v", len(got), err)
		}
	})
}

func TestPlanRepoUpdate(t *testing.T) {
	t.Run("dado un plan, cuando se renombra, edita un medicamento, agrega otro y quita otro, entonces se conservan los ids existentes", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p := d.givenPlan(t, ana, "Enalapril", "Aspirina")
		idEnalapril := p.Medications[0].ID

		cargado, _ := d.plans.ByID(ctx, p.ID)
		cargado.Name = "Nuevo nombre"
		cargado.Medications[0].Dose = "20 mg"
		cargado.Medications[0].Schedules = []domain.Schedule{{DayOfWeek: 6, TimeOfDay: "07:15"}}
		cargado.Medications = append(cargado.Medications[:1], medication("Atorvastatina")) // quita Aspirina, agrega nuevo
		if err := d.plans.Update(ctx, cargado); err != nil {
			t.Fatal(err)
		}

		got, _ := d.plans.ByID(ctx, p.ID)
		if got.Name != "Nuevo nombre" || len(got.Medications) != 2 {
			t.Fatalf("plan inesperado: %+v", got)
		}
		if got.Medications[0].ID != idEnalapril || got.Medications[0].Dose != "20 mg" ||
			len(got.Medications[0].Schedules) != 1 || got.Medications[0].Schedules[0].TimeOfDay != "07:15" {
			t.Fatalf("el medicamento editado no se conservó bien: %+v", got.Medications[0])
		}
		if got.Medications[1].Name != "Atorvastatina" || got.Medications[1].ID == "" {
			t.Fatalf("el medicamento nuevo no quedó: %+v", got.Medications[1])
		}
	})

	t.Run("dado un medicamento que no es del plan, cuando se actualiza, entonces no se encuentra y no cambia nada", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p := d.givenPlan(t, ana, "Enalapril")
		cargado, _ := d.plans.ByID(ctx, p.ID)
		cargado.Name = "No debería guardarse"
		cargado.Medications[0].ID = "00000000-0000-0000-0000-000000000000"
		if err := d.plans.Update(ctx, cargado); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
		if got, _ := d.plans.ByID(ctx, p.ID); got.Name != "Hipertensión" {
			t.Fatalf("la transacción debió revertirse, nombre=%q", got.Name)
		}
	})

	t.Run("dado un plan inexistente, cuando se actualiza o se elimina, entonces no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		fantasma := &domain.Plan{ID: "00000000-0000-0000-0000-000000000000", Name: "x", Status: domain.PlanActive}
		if err := d.plans.Update(ctx, fantasma); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("update: got %v", err)
		}
		if err := d.plans.Delete(ctx, fantasma.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("delete: got %v", err)
		}
	})
}

func TestPlanRepoDelete(t *testing.T) {
	t.Run("dado un plan con medicamentos y horarios, cuando se elimina, entonces desaparece junto con todo lo suyo", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p := d.givenPlan(t, ana, "Enalapril", "Aspirina")
		if err := d.plans.Delete(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.plans.ByID(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
		var meds, scheds int
		_ = d.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM medications), (SELECT count(*) FROM schedules)").Scan(&meds, &scheds)
		if meds != 0 || scheds != 0 {
			t.Fatalf("quedaron huérfanos: %d medicamentos, %d horarios", meds, scheds)
		}
	})
}

// ---- invitaciones y cuidadores ----

func TestInviteRepo(t *testing.T) {
	t.Run("dada una invitación, cuando se guarda y se busca por id, por hash del token y por plan, entonces vuelve igual y sin cuidador", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p := d.givenPlan(t, ana, "Enalapril")
		c := d.givenInvite(t, p.ID, "carla@mail.com", "hash-1")
		porID, err1 := d.invites.ByID(ctx, c.ID)
		porHash, err2 := d.invites.ByTokenHash(ctx, "hash-1")
		porPlan, err3 := d.invites.ByPlan(ctx, p.ID)
		if err1 != nil || err2 != nil || err3 != nil || len(porPlan) != 1 {
			t.Fatalf("errores: %v %v %v", err1, err2, err3)
		}
		for _, got := range []*domain.PlanCaregiver{porID, porHash, porPlan[0]} {
			if got.ID != c.ID || got.PlanID != p.ID || got.InviteEmail != "carla@mail.com" || got.CaregiverID != "" ||
				got.Status != domain.InvitePending || got.TokenHash != "hash-1" || !got.ExpiresAt.Equal(c.ExpiresAt.Truncate(time.Microsecond)) {
				t.Fatalf("invitación inesperada: %+v", got)
			}
		}
	})

	t.Run("dado un id o hash inexistente, cuando se busca, entonces no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		if _, err := d.invites.ByID(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("id inválido: got %v", err)
		}
		if _, err := d.invites.ByTokenHash(ctx, "no-existe"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("hash: got %v", err)
		}
	})

	t.Run("dado un email ya invitado a un plan, cuando se lo invita de nuevo, entonces ya está invitado; tras rechazarla, se permite", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		primera := d.givenInvite(t, p.ID, "carla@mail.com", "hash-1")
		dup := &domain.PlanCaregiver{PlanID: p.ID, InviteEmail: "carla@mail.com", Status: domain.InvitePending, TokenHash: "hash-2", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}
		if err := d.invites.Create(ctx, dup); !errors.Is(err, domain.ErrAlreadyInvited) {
			t.Fatalf("esperaba ErrAlreadyInvited, got %v", err)
		}
		primera.Status = domain.InviteRejected
		if err := d.invites.Update(ctx, primera); err != nil {
			t.Fatal(err)
		}
		if err := d.invites.Create(ctx, dup); err != nil {
			t.Fatalf("tras el rechazo debería poder invitarse: %v", err)
		}
	})

	t.Run("dadas invitaciones a un email, cuando se listan las pendientes, entonces solo vienen las pendientes de ese email", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p1, p2, p3 := d.givenPlan(t, ana, "A"), d.givenPlan(t, ana, "B"), d.givenPlan(t, ana, "C")
		d.givenInvite(t, p1.ID, "carla@mail.com", "h1")
		rechazada := d.givenInvite(t, p2.ID, "carla@mail.com", "h2")
		d.givenInvite(t, p3.ID, "otro@mail.com", "h3")
		rechazada.Status = domain.InviteRejected
		_ = d.invites.Update(ctx, rechazada)
		got, err := d.invites.PendingByEmail(ctx, "carla@mail.com")
		if err != nil || len(got) != 1 || got[0].PlanID != p1.ID {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("dada una invitación aceptada por Carla, cuando se consulta quién cuida, entonces Carla cuida solo ese plan", func(t *testing.T) {
		d := givenDB(t)
		ana, carla := d.givenUser(t, "ana"), d.givenUser(t, "carla")
		suyo, otro := d.givenPlan(t, ana, "A"), d.givenPlan(t, ana, "B")
		c := d.givenInvite(t, suyo.ID, "carla@mail.com", "h1")

		antes, _ := d.invites.IsAcceptedCaregiver(ctx, carla.ID, suyo.ID)
		hayAntes, _ := d.invites.HasAcceptedCaregiver(ctx, suyo.ID)
		if antes || hayAntes {
			t.Fatal("una invitación pendiente no debería contar como cuidador")
		}

		c.CaregiverID, c.Status = carla.ID, domain.InviteAccepted
		if err := d.invites.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		esSuyo, _ := d.invites.IsAcceptedCaregiver(ctx, carla.ID, suyo.ID)
		esOtro, _ := d.invites.IsAcceptedCaregiver(ctx, carla.ID, otro.ID)
		hay, _ := d.invites.HasAcceptedCaregiver(ctx, suyo.ID)
		hayOtro, _ := d.invites.HasAcceptedCaregiver(ctx, otro.ID)
		quienes, _ := d.invites.AcceptedCaregiverIDs(ctx, suyo.ID)
		if len(quienes) != 1 || quienes[0] != carla.ID {
			t.Fatalf("AcceptedCaregiverIDs: %v", quienes)
		}
		ids, err := d.invites.SupervisedPlanIDs(ctx, carla.ID)
		if !esSuyo || esOtro || !hay || hayOtro || err != nil || len(ids) != 1 || ids[0] != suyo.ID {
			t.Fatalf("permisos incorrectos: suyo=%v otro=%v hay=%v hayOtro=%v ids=%v err=%v", esSuyo, esOtro, hay, hayOtro, ids, err)
		}
	})

	t.Run("dada una invitación, cuando se elimina, entonces desaparece; y si se elimina el plan, se van sus vínculos", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		p1, p2 := d.givenPlan(t, ana, "A"), d.givenPlan(t, ana, "B")
		c1 := d.givenInvite(t, p1.ID, "carla@mail.com", "h1")
		c2 := d.givenInvite(t, p2.ID, "carla@mail.com", "h2")
		if err := d.invites.Delete(ctx, c1.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.invites.ByID(ctx, c1.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("c1 debería no existir, got %v", err)
		}
		_ = d.plans.Delete(ctx, p2.ID)
		if _, err := d.invites.ByID(ctx, c2.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("c2 debería haberse ido con su plan, got %v", err)
		}
	})
}

// ---- tomas (dose_logs) y zona horaria ----

func TestDoseRepo(t *testing.T) {
	scheduled := time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC)

	t.Run("dada una toma, cuando se guarda y se busca por medicamento y hora, entonces vuelve igual", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		medID := p.Medications[0].ID
		taken := scheduled.Add(10 * time.Minute)
		l := &domain.DoseLog{MedicationID: medID, ScheduledAt: scheduled, TakenAt: &taken, Status: domain.DoseTaken}
		if err := d.doses.Save(ctx, l); err != nil || l.ID == "" {
			t.Fatalf("save: id=%q err=%v", l.ID, err)
		}
		got, err := d.doses.Get(ctx, medID, scheduled)
		if err != nil || got.ID != l.ID || got.Status != domain.DoseTaken || got.TakenAt == nil || !got.TakenAt.Equal(taken) || !got.ScheduledAt.Equal(scheduled) {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("dada una toma omitida, cuando se guarda otra vez como tomada, entonces se actualiza la misma fila", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		medID := p.Medications[0].ID
		first := &domain.DoseLog{MedicationID: medID, ScheduledAt: scheduled, Status: domain.DoseMissed}
		_ = d.doses.Save(ctx, first)
		taken := scheduled.Add(time.Hour)
		second := &domain.DoseLog{MedicationID: medID, ScheduledAt: scheduled, TakenAt: &taken, Status: domain.DoseTaken}
		if err := d.doses.Save(ctx, second); err != nil {
			t.Fatal(err)
		}
		var n int
		_ = d.pool.QueryRow(ctx, "SELECT count(*) FROM dose_logs").Scan(&n)
		got, _ := d.doses.Get(ctx, medID, scheduled)
		if n != 1 || got.Status != domain.DoseTaken || second.ID != first.ID {
			t.Fatalf("filas=%d got=%+v ids %q/%q", n, got, first.ID, second.ID)
		}
	})

	t.Run("dada una toma inexistente, cuando se busca, entonces no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		if _, err := d.doses.Get(ctx, "00000000-0000-0000-0000-000000000000", scheduled); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
		if _, err := d.doses.Get(ctx, "nope", scheduled); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("id inválido: got %v", err)
		}
	})

	t.Run("dadas tomas en distintos días y medicamentos, cuando se consulta una ventana, entonces vienen solo las de esa ventana [desde, hasta)", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril", "Aspirina")
		m1, m2 := p.Medications[0].ID, p.Medications[1].ID
		for _, l := range []*domain.DoseLog{
			{MedicationID: m1, ScheduledAt: scheduled, Status: domain.DoseMissed},
			{MedicationID: m1, ScheduledAt: scheduled.AddDate(0, 0, 3), Status: domain.DoseMissed},
			{MedicationID: m2, ScheduledAt: scheduled, Status: domain.DoseMissed},
		} {
			_ = d.doses.Save(ctx, l)
		}
		got, err := d.doses.Between(ctx, []string{m1, m2}, scheduled, scheduled.AddDate(0, 0, 3))
		if err != nil || len(got) != 2 {
			t.Fatalf("esperaba 2 (la del día 12 queda afuera), got %d err=%v", len(got), err)
		}
		if solo, _ := d.doses.Between(ctx, []string{m2}, scheduled, scheduled.AddDate(0, 0, 9)); len(solo) != 1 {
			t.Fatalf("filtrando por medicamento esperaba 1, got %d", len(solo))
		}
		if vacio, err := d.doses.Between(ctx, nil, scheduled, scheduled.AddDate(0, 0, 9)); err != nil || len(vacio) != 0 {
			t.Fatalf("sin medicamentos: %d %v", len(vacio), err)
		}
	})

	t.Run("dado un plan eliminado, cuando se consultan sus tomas, entonces también se eliminaron", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		_ = d.doses.Save(ctx, &domain.DoseLog{MedicationID: p.Medications[0].ID, ScheduledAt: scheduled, Status: domain.DoseMissed})
		_ = d.plans.Delete(ctx, p.ID)
		var n int
		_ = d.pool.QueryRow(ctx, "SELECT count(*) FROM dose_logs").Scan(&n)
		if n != 0 {
			t.Fatalf("quedaron %d tomas huérfanas", n)
		}
	})
}

func TestTimezoneOf(t *testing.T) {
	t.Run("dado un usuario con zona horaria, cuando se consulta, entonces se devuelve; si no existe, no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		u := d.givenUser(t, "ana")
		_ = d.users.SetTimezone(ctx, u.ID, "America/Montevideo")
		tz, err := d.users.TimezoneOf(ctx, u.ID)
		if err != nil || tz != "America/Montevideo" {
			t.Fatalf("got %q err=%v", tz, err)
		}
		if _, err := d.users.TimezoneOf(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

// ---- dispositivos y avisos ----

func TestDeviceRepo(t *testing.T) {
	t.Run("dado un token, cuando se registra dos veces, entonces queda una sola fila", func(t *testing.T) {
		d := givenDB(t)
		ana := d.givenUser(t, "ana")
		_ = d.devices.Upsert(ctx, ana.ID, "tok-1", "android")
		_ = d.devices.Upsert(ctx, ana.ID, "tok-1", "android")
		toks, err := d.devices.TokensOf(ctx, ana.ID)
		if err != nil || len(toks) != 1 || toks[0] != "tok-1" {
			t.Fatalf("got %v err=%v", toks, err)
		}
	})

	t.Run("dado un celular que pasa de un usuario a otro, cuando el nuevo lo registra, entonces el anterior deja de tenerlo", func(t *testing.T) {
		d := givenDB(t)
		ana, carla := d.givenUser(t, "ana"), d.givenUser(t, "carla")
		_ = d.devices.Upsert(ctx, ana.ID, "tok-1", "android")
		_ = d.devices.Upsert(ctx, carla.ID, "tok-1", "android")
		deAna, _ := d.devices.TokensOf(ctx, ana.ID)
		deCarla, _ := d.devices.TokensOf(ctx, carla.ID)
		if len(deAna) != 0 || len(deCarla) != 1 {
			t.Fatalf("ana=%v carla=%v", deAna, deCarla)
		}
	})

	t.Run("dado un token, cuando lo borra su dueño se va; cuando intenta borrarlo otro usuario, no pasa nada; y se pueden borrar varios a la vez", func(t *testing.T) {
		d := givenDB(t)
		ana, carla := d.givenUser(t, "ana"), d.givenUser(t, "carla")
		_ = d.devices.Upsert(ctx, carla.ID, "tok-1", "android")
		_ = d.devices.Upsert(ctx, carla.ID, "tok-2", "android")
		_ = d.devices.Upsert(ctx, carla.ID, "tok-3", "android")
		_ = d.devices.Delete(ctx, ana.ID, "tok-1")
		if toks, _ := d.devices.TokensOf(ctx, carla.ID); len(toks) != 3 {
			t.Fatalf("Ana no debería poder borrarlo: %v", toks)
		}
		_ = d.devices.Delete(ctx, carla.ID, "tok-1")
		_ = d.devices.DeleteTokens(ctx, []string{"tok-2", "tok-3", "no-existe"})
		if toks, _ := d.devices.TokensOf(ctx, carla.ID); len(toks) != 0 {
			t.Fatalf("debería quedar vacío: %v", toks)
		}
	})
}

func TestNotificationRepo(t *testing.T) {
	nueva := func(userID, body string) *domain.Notification {
		return &domain.Notification{UserID: userID, Kind: domain.NotifMissedDose, Title: "Dosis no registrada", Body: body,
			Data: map[string]string{"planId": "p1", "scheduledAt": "2026-11-12T23:30:00Z"}, CreatedAt: time.Now()}
	}

	t.Run("dados avisos de dos usuarios, cuando Carla lista los suyos, entonces ve solo los suyos, el más nuevo primero y con todo su contenido", func(t *testing.T) {
		d := givenDB(t)
		ana, carla := d.givenUser(t, "ana"), d.givenUser(t, "carla")
		for _, n := range []*domain.Notification{nueva(carla.ID, "primero"), nueva(ana.ID, "de ana"), nueva(carla.ID, "segundo")} {
			if err := d.notifs.Create(ctx, n); err != nil || n.ID == "" {
				t.Fatalf("create: %v", err)
			}
			time.Sleep(2 * time.Millisecond) // separa los created_at
		}
		got, err := d.notifs.ListByUser(ctx, carla.ID, 10)
		if err != nil || len(got) != 2 || got[0].Body != "segundo" || got[1].Body != "primero" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if got[0].Kind != domain.NotifMissedDose || got[0].Title != "Dosis no registrada" || got[0].Data["planId"] != "p1" || got[0].Data["scheduledAt"] != "2026-11-12T23:30:00Z" || got[0].ReadAt != nil {
			t.Fatalf("contenido incorrecto: %+v", got[0])
		}
		if limitado, _ := d.notifs.ListByUser(ctx, carla.ID, 1); len(limitado) != 1 {
			t.Fatalf("el límite no se respetó: %d", len(limitado))
		}
	})

	t.Run("dado un aviso, cuando su dueña lo marca como leído, entonces queda leído; cuando otro usuario lo intenta o el id es inválido, no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		ana, carla := d.givenUser(t, "ana"), d.givenUser(t, "carla")
		n := nueva(carla.ID, "x")
		_ = d.notifs.Create(ctx, n)
		if err := d.notifs.MarkRead(ctx, ana.ID, n.ID, time.Now()); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("ajeno: got %v", err)
		}
		if err := d.notifs.MarkRead(ctx, carla.ID, "nope", time.Now()); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("id inválido: got %v", err)
		}
		if err := d.notifs.MarkRead(ctx, carla.ID, n.ID, time.Now()); err != nil {
			t.Fatal(err)
		}
		got, _ := d.notifs.ListByUser(ctx, carla.ID, 10)
		if got[0].ReadAt == nil {
			t.Fatal("debería figurar como leído")
		}
	})
}

func TestMarkMissed(t *testing.T) {
	at := time.Date(2026, 11, 12, 23, 30, 0, 0, time.UTC)

	t.Run("dada una toma sin registro, cuando se marca omitida, entonces se crea; la segunda vez no cambia nada", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		med := p.Medications[0].ID
		first, err1 := d.doses.MarkMissed(ctx, med, at)
		second, err2 := d.doses.MarkMissed(ctx, med, at)
		if err1 != nil || err2 != nil || !first || second {
			t.Fatalf("first=%v second=%v err=%v/%v", first, second, err1, err2)
		}
		got, _ := d.doses.Get(ctx, med, at)
		if got.Status != domain.DoseMissed || got.TakenAt != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("dada una toma que ya se registró como tomada, cuando se intenta marcar omitida, entonces se respeta que fue tomada", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		med := p.Medications[0].ID
		taken := at.Add(5 * time.Minute)
		_ = d.doses.Save(ctx, &domain.DoseLog{MedicationID: med, ScheduledAt: at, TakenAt: &taken, Status: domain.DoseTaken})
		created, err := d.doses.MarkMissed(ctx, med, at)
		got, _ := d.doses.Get(ctx, med, at)
		if err != nil || created || got.Status != domain.DoseTaken {
			t.Fatalf("created=%v got=%+v err=%v", created, got, err)
		}
	})
}

func TestReminderRepo(t *testing.T) {
	t.Run("dados planes con y sin cuidadores aceptados, cuando se consultan los activos, entonces vienen solo los que tienen cuidador, con email, zona horaria y medicamentos", func(t *testing.T) {
		d := givenDB(t)
		ana, carla, dario := d.givenUser(t, "ana"), d.givenUser(t, "carla"), d.givenUser(t, "dario")
		_ = d.users.SetTimezone(ctx, ana.ID, "America/Montevideo")

		conCuidadores := d.givenPlan(t, ana, "Enalapril", "Aspirina")
		soloPendiente := d.givenPlan(t, ana, "X")
		sinNadie := d.givenPlan(t, ana, "Y")
		_ = sinNadie
		for _, u := range []*domain.User{carla, dario} {
			c := d.givenInvite(t, conCuidadores.ID, u.Email, "h-"+u.FirebaseUID)
			c.CaregiverID, c.Status = u.ID, domain.InviteAccepted
			_ = d.invites.Update(ctx, c)
		}
		d.givenInvite(t, soloPendiente.ID, "carla@mail.com", "h-pend") // pendiente: no cuenta

		got, err := d.remind.ActivePlansWithCaregivers(ctx)
		if err != nil || len(got) != 1 {
			t.Fatalf("esperaba 1 plan, got %d err=%v", len(got), err)
		}
		ap := got[0]
		if ap.Plan.ID != conCuidadores.ID || ap.PatientEmail != "ana@mail.com" || ap.Timezone != "America/Montevideo" ||
			len(ap.CaregiverIDs) != 2 || len(ap.Plan.Medications) != 2 || len(ap.Plan.Medications[0].Schedules) != 2 {
			t.Fatalf("plan incorrecto: %+v", ap)
		}
	})

	t.Run("dado un plan completado o cancelado con cuidador, cuando se consultan los activos, entonces no aparece", func(t *testing.T) {
		d := givenDB(t)
		ana, carla := d.givenUser(t, "ana"), d.givenUser(t, "carla")
		p := d.givenPlan(t, ana, "Enalapril")
		c := d.givenInvite(t, p.ID, carla.Email, "h1")
		c.CaregiverID, c.Status = carla.ID, domain.InviteAccepted
		_ = d.invites.Update(ctx, c)
		for _, status := range []string{"completed", "cancelled"} {
			_, _ = d.pool.Exec(ctx, "UPDATE treatment_plans SET status = $1::plan_status", status)
			if got, _ := d.remind.ActivePlansWithCaregivers(ctx); len(got) != 0 {
				t.Errorf("%s: no debería aparecer, got %d", status, len(got))
			}
		}
	})
}

// ---- stock ----

func TestStockRepo(t *testing.T) {
	givenMed := func(t *testing.T, d db, units int) string {
		t.Helper()
		p := &domain.Plan{PatientID: d.givenUser(t, "ana").ID, Name: "P", Status: domain.PlanActive, CreatedAt: time.Now()}
		m := medication("Enalapril")
		m.StockUnits = units
		p.Medications = []domain.Medication{m}
		if err := d.plans.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p.Medications[0].ID
	}

	t.Run("dado un stock de 5, cuando se descuenta una unidad, entonces devuelve antes y después y queda guardado", func(t *testing.T) {
		d := givenDB(t)
		id := givenMed(t, d, 5)
		before, after, err := d.stock.Consume(ctx, id, 1)
		if err != nil || before != 5 || after != 4 {
			t.Fatalf("before=%d after=%d err=%v", before, after, err)
		}
		var stored int
		_ = d.pool.QueryRow(ctx, "SELECT stock_units FROM medications WHERE id = $1::uuid", id).Scan(&stored)
		if stored != 4 {
			t.Fatalf("guardado: %d", stored)
		}
	})

	t.Run("dado un stock de 1, cuando se descuentan 3 unidades, entonces queda en 0 y nunca negativo", func(t *testing.T) {
		d := givenDB(t)
		id := givenMed(t, d, 1)
		before, after, err := d.stock.Consume(ctx, id, 3)
		if err != nil || before != 1 || after != 0 {
			t.Fatalf("before=%d after=%d err=%v", before, after, err)
		}
		before, after, _ = d.stock.Consume(ctx, id, 1)
		if before != 0 || after != 0 {
			t.Fatalf("en 0 debería seguir en 0: %d→%d", before, after)
		}
	})

	t.Run("dado un stock de 10 y 25 tomas simultáneas, cuando se descuenta en paralelo, entonces nunca queda negativo y exactamente 10 vieron stock disponible", func(t *testing.T) {
		d := givenDB(t)
		id := givenMed(t, d, 10)
		var wg sync.WaitGroup
		var mu sync.Mutex
		conStock := 0
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				before, _, err := d.stock.Consume(ctx, id, 1)
				if err != nil {
					t.Error(err)
					return
				}
				if before > 0 {
					mu.Lock()
					conStock++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		var stored int
		_ = d.pool.QueryRow(ctx, "SELECT stock_units FROM medications WHERE id = $1::uuid", id).Scan(&stored)
		if stored != 0 || conStock != 10 {
			t.Fatalf("stock final=%d, con stock=%d (esperaba 0 y 10)", stored, conStock)
		}
	})

	t.Run("dado un medicamento, cuando se reponen unidades, entonces se suman; con un id inexistente o inválido no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		id := givenMed(t, d, 3)
		if total, err := d.stock.Add(ctx, id, 30); err != nil || total != 33 {
			t.Fatalf("total=%d err=%v", total, err)
		}
		for _, bad := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
			if _, err := d.stock.Add(ctx, bad, 1); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("Add %q: %v", bad, err)
			}
			if _, _, err := d.stock.Consume(ctx, bad, 1); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("Consume %q: %v", bad, err)
			}
		}
	})
}

func TestPlanUpdateAndStock(t *testing.T) {
	stockOf := func(d db, medID string) int {
		var n int
		_ = d.pool.QueryRow(ctx, "SELECT stock_units FROM medications WHERE id = $1::uuid", medID).Scan(&n)
		return n
	}

	t.Run("dado un plan leído con stock 30 y un descuento posterior (29), cuando se guarda una edición con los datos viejos, entonces el descuento NO se pierde", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		medID := p.Medications[0].ID
		viejo, _ := d.plans.ByID(ctx, p.ID) // stock 30
		if _, after, _ := d.stock.Consume(ctx, medID, 1); after != 29 {
			t.Fatalf("descuento: %d", after)
		}
		viejo.Medications[0].Dose = "20 mg"
		viejo.Name = "Renombrado"
		if err := d.plans.Update(ctx, viejo); err != nil {
			t.Fatal(err)
		}
		got, _ := d.plans.ByID(ctx, p.ID)
		if got.Medications[0].Dose != "20 mg" || got.Name != "Renombrado" || stockOf(d, medID) != 29 {
			t.Fatalf("dosis=%q nombre=%q stock=%d (esperaba 20 mg, Renombrado, 29)", got.Medications[0].Dose, got.Name, stockOf(d, medID))
		}
	})

	t.Run("dado un medicamento nuevo agregado en una edición, cuando se guarda, entonces conserva el stock inicial indicado", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		cargado, _ := d.plans.ByID(ctx, p.ID)
		nuevo := medication("Aspirina")
		nuevo.StockUnits = 12
		cargado.Medications = append(cargado.Medications, nuevo)
		if err := d.plans.Update(ctx, cargado); err != nil {
			t.Fatal(err)
		}
		got, _ := d.plans.ByID(ctx, p.ID)
		if len(got.Medications) != 2 || got.Medications[1].StockUnits != 12 {
			t.Fatalf("got %+v", got.Medications)
		}
	})

	t.Run("dado un ajuste explícito, cuando se fija el stock, entonces queda ese valor exacto; con un id inexistente o inválido no se encuentra", func(t *testing.T) {
		d := givenDB(t)
		p := d.givenPlan(t, d.givenUser(t, "ana"), "Enalapril")
		medID := p.Medications[0].ID
		if err := d.plans.SetMedicationStock(ctx, medID, 7); err != nil || stockOf(d, medID) != 7 {
			t.Fatalf("err=%v stock=%d", err, stockOf(d, medID))
		}
		for _, bad := range []string{"00000000-0000-0000-0000-000000000000", "nope"} {
			if err := d.plans.SetMedicationStock(ctx, bad, 1); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%q: got %v", bad, err)
			}
		}
	})
}
