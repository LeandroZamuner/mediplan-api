package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/platform/token"
	"github.com/lea/mediplan-api/internal/usecase/auth"
	"github.com/lea/mediplan-api/internal/usecase/caregivers"
	"github.com/lea/mediplan-api/internal/usecase/plans"
)

// ---- fakes en memoria ----

type planRepo struct {
	byID map[string]*domain.Plan
	seq  int
}

func (f *planRepo) store(p *domain.Plan) {
	for i := range p.Medications {
		if p.Medications[i].ID == "" {
			f.seq++
			p.Medications[i].ID = fmt.Sprintf("m%d", f.seq)
		}
	}
	c := *p
	c.Medications = append([]domain.Medication(nil), p.Medications...)
	f.byID[p.ID] = &c
}
func (f *planRepo) Create(_ context.Context, p *domain.Plan) error {
	f.seq++
	p.ID = fmt.Sprintf("p%d", f.seq)
	f.store(p)
	return nil
}
func (f *planRepo) Update(_ context.Context, p *domain.Plan) error {
	if old, ok := f.byID[p.ID]; ok { // como el repositorio real: no pisa el stock existente
		stored := map[string]int{}
		for _, m := range old.Medications {
			stored[m.ID] = m.StockUnits
		}
		for i := range p.Medications {
			if u, exists := stored[p.Medications[i].ID]; exists {
				p.Medications[i].StockUnits = u
			}
		}
	}
	f.store(p)
	return nil
}
func (f *planRepo) SetMedicationStock(_ context.Context, medID string, units int) error {
	for _, p := range f.byID {
		for i := range p.Medications {
			if p.Medications[i].ID == medID {
				p.Medications[i].StockUnits = units
				return nil
			}
		}
	}
	return domain.ErrNotFound
}
func (f *planRepo) Delete(_ context.Context, id string) error { delete(f.byID, id); return nil }
func (f *planRepo) ByID(_ context.Context, id string) (*domain.Plan, error) {
	if p, ok := f.byID[id]; ok {
		c := *p
		c.Medications = append([]domain.Medication(nil), p.Medications...)
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (f *planRepo) ByPatient(_ context.Context, pid string) ([]*domain.Plan, error) {
	var out []*domain.Plan
	for _, p := range f.byID {
		if p.PatientID == pid {
			c := *p
			out = append(out, &c)
		}
	}
	return out, nil
}

// inviteRepo implementa a la vez caregivers.InviteRepo y plans.CaregiverLinks,
// como hará el adaptador de Postgres (misma tabla plan_caregivers).
type inviteRepo struct {
	rows map[string]*domain.PlanCaregiver
	seq  int
}

func (f *inviteRepo) filter(match func(*domain.PlanCaregiver) bool) []*domain.PlanCaregiver {
	var out []*domain.PlanCaregiver
	for _, r := range f.rows {
		if match(r) {
			c := *r
			out = append(out, &c)
		}
	}
	return out
}
func (f *inviteRepo) Create(_ context.Context, c *domain.PlanCaregiver) error {
	f.seq++
	c.ID = fmt.Sprintf("l%d", f.seq)
	cp := *c
	f.rows[c.ID] = &cp
	return nil
}
func (f *inviteRepo) ByID(_ context.Context, id string) (*domain.PlanCaregiver, error) {
	if r, ok := f.rows[id]; ok {
		c := *r
		return &c, nil
	}
	return nil, domain.ErrNotFound
}
func (f *inviteRepo) ByTokenHash(_ context.Context, h string) (*domain.PlanCaregiver, error) {
	if r := f.filter(func(c *domain.PlanCaregiver) bool { return c.TokenHash == h }); len(r) > 0 {
		return r[0], nil
	}
	return nil, domain.ErrNotFound
}
func (f *inviteRepo) ByPlan(_ context.Context, planID string) ([]*domain.PlanCaregiver, error) {
	return f.filter(func(c *domain.PlanCaregiver) bool { return c.PlanID == planID }), nil
}
func (f *inviteRepo) PendingByEmail(_ context.Context, email string) ([]*domain.PlanCaregiver, error) {
	return f.filter(func(c *domain.PlanCaregiver) bool { return c.Status == domain.InvitePending && c.InviteEmail == email }), nil
}
func (f *inviteRepo) Update(_ context.Context, c *domain.PlanCaregiver) error {
	cp := *c
	f.rows[c.ID] = &cp
	return nil
}
func (f *inviteRepo) Delete(_ context.Context, id string) error { delete(f.rows, id); return nil }

func (f *inviteRepo) IsAcceptedCaregiver(_ context.Context, userID, planID string) (bool, error) {
	return len(f.filter(func(c *domain.PlanCaregiver) bool {
		return c.PlanID == planID && c.CaregiverID == userID && c.Status == domain.InviteAccepted
	})) > 0, nil
}
func (f *inviteRepo) HasAcceptedCaregiver(_ context.Context, planID string) (bool, error) {
	return len(f.filter(func(c *domain.PlanCaregiver) bool {
		return c.PlanID == planID && c.Status == domain.InviteAccepted
	})) > 0, nil
}
func (f *inviteRepo) AcceptedCaregiverIDs(_ context.Context, planID string) ([]string, error) {
	var ids []string
	for _, c := range f.filter(func(c *domain.PlanCaregiver) bool { return c.PlanID == planID && c.Status == domain.InviteAccepted }) {
		ids = append(ids, c.CaregiverID)
	}
	return ids, nil
}
func (f *inviteRepo) SupervisedPlanIDs(_ context.Context, userID string) ([]string, error) {
	var ids []string
	for _, c := range f.filter(func(c *domain.PlanCaregiver) bool {
		return c.CaregiverID == userID && c.Status == domain.InviteAccepted
	}) {
		ids = append(ids, c.PlanID)
	}
	return ids, nil
}

// ---- given ----

// givenAPI: personas ana, lucia, carla y dario, todas con email verificado (<nombre>@mail.com).
// El token de sesión de cada una es su propio nombre. Nadie es "cuidador" de entrada:
// serlo depende de haber aceptado una invitación a un plan concreto.
func givenAPI() http.Handler {
	ids := verifier{}
	users := repo{}
	for _, id := range []string{"ana", "lucia", "carla", "dario"} {
		ids[id] = &auth.Identity{UID: id, Email: id + "@mail.com", EmailVerified: true}
		users[id] = &domain.User{ID: id, FirebaseUID: id, Email: id + "@mail.com", EmailVerified: true, Role: domain.RolePatient}
	}
	ids["eva"] = &auth.Identity{UID: "eva", Email: "eva@mail.com", EmailVerified: false}
	users["eva"] = &domain.User{ID: "eva", FirebaseUID: "eva", Email: "eva@mail.com", EmailVerified: false, Role: domain.RolePatient}

	links := &inviteRepo{rows: map[string]*domain.PlanCaregiver{}}
	planRepo := &planRepo{byID: map[string]*domain.Plan{}}
	return NewRouter(Handlers{
		Auth:       NewAuthHandler(auth.NewService(ids, users)),
		Plans:      NewPlansHandler(plans.NewService(planRepo, links)),
		Caregivers: NewCaregiversHandler(caregivers.NewService(links, planRepo, token.Generator{})),
	})
}

const planBody = `{"name":"Hipertensión","medications":[{"name":"Enalapril","dose":"10 mg","startDate":"2026-11-01",
 "schedules":[{"dayOfWeek":1,"timeOfDay":"08:00"}],"stockUnits":30,"stockAlertAt":5},
 {"name":"Aspirina","dose":"100 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":2,"timeOfDay":"09:00"}]}]}`

const medBody = `{"name":"Atorvastatina","dose":"20 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":3,"timeOfDay":"21:00"}]}`

type planJSON struct {
	ID          string `json:"id"`
	PatientID   string `json:"patientId"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Medications []struct {
		ID, Name, Dose string
	} `json:"medications"`
}

func decodePlan(t *testing.T, w *httptest.ResponseRecorder) planJSON {
	t.Helper()
	var p planJSON
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("respuesta no es un plan: %s", w.Body.String())
	}
	return p
}

// givenPlanOf crea un plan por HTTP, hecho por su dueño.
func givenPlanOf(t *testing.T, h http.Handler, owner string) planJSON {
	t.Helper()
	w := do(h, "POST", "/api/v1/plans", owner, planBody)
	if w.Code != 201 {
		t.Fatalf("no se pudo crear el plan: %d %s", w.Code, w.Body.String())
	}
	return decodePlan(t, w)
}

// givenCaregiver: el dueño invita al email de la persona y ella acepta con el enlace (flujo real).
func givenCaregiver(t *testing.T, h http.Handler, owner, planID, caregiver string) {
	t.Helper()
	w := do(h, "POST", "/api/v1/plans/"+planID+"/caregivers", owner, `{"email":"`+caregiver+`@mail.com"}`)
	if w.Code != 201 {
		t.Fatalf("no se pudo invitar: %d %s", w.Code, w.Body.String())
	}
	var inv struct{ Token string }
	_ = json.Unmarshal(w.Body.Bytes(), &inv)
	if a := do(h, "POST", "/api/v1/invitations/accept", caregiver, `{"token":"`+inv.Token+`"}`); a.Code != 200 {
		t.Fatalf("no se pudo aceptar: %d %s", a.Code, a.Body.String())
	}
}

// ---- tests ----

func TestCreatePlanHTTP(t *testing.T) {
	t.Run("dado un usuario autenticado, cuando crea un plan válido, entonces responde 201 con el plan a su nombre", func(t *testing.T) {
		h := givenAPI()
		for _, u := range []string{"lucia", "carla"} { // cualquiera puede tener planes propios
			w := do(h, "POST", "/api/v1/plans", u, planBody)
			p := decodePlan(t, w)
			if w.Code != 201 || p.PatientID != u || p.Status != "active" || len(p.Medications) != 2 || p.ID == "" {
				t.Fatalf("%s: got %d %+v", u, w.Code, p)
			}
		}
	})

	cases := []struct {
		name, token, body string
		want              int
	}{
		{"dado que no hay token, cuando crea un plan, entonces 401", "", planBody, 401},
		{"dado un medicamento sin dosis, cuando crea un plan, entonces 422", "lucia", strings.Replace(planBody, `"dose":"10 mg"`, `"dose":""`, 1), 422},
		{"dado una fecha mal formada, cuando crea un plan, entonces 400", "lucia", strings.Replace(planBody, "2026-11-01", "01/11/2026", 1), 400},
		{"dado un JSON roto, cuando crea un plan, entonces 400", "lucia", `{`, 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := do(givenAPI(), "POST", "/api/v1/plans", c.token, c.body); w.Code != c.want {
				t.Fatalf("esperaba %d, got %d (%s)", c.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestReadPlansHTTP(t *testing.T) {
	t.Run("dado un plan de Ana cuidado por Carla, cuando lo piden Ana y Carla, entonces 200; Darío 403; uno inexistente 404", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", p.ID, "carla")
		for tok, want := range map[string]int{"ana": 200, "carla": 200, "dario": 403, "lucia": 403} {
			if w := do(h, "GET", "/api/v1/plans/"+p.ID, tok, ""); w.Code != want {
				t.Errorf("%s: esperaba %d, got %d", tok, want, w.Code)
			}
		}
		if w := do(h, "GET", "/api/v1/plans/nope", "ana", ""); w.Code != 404 {
			t.Errorf("inexistente: esperaba 404, got %d", w.Code)
		}
	})

	t.Run("dado que Carla cuida solo UN plan de Ana, cuando pide el otro plan de Ana, entonces 403", func(t *testing.T) {
		h := givenAPI()
		suyo := givenPlanOf(t, h, "ana")
		otro := givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", suyo.ID, "carla")
		if w := do(h, "GET", "/api/v1/plans/"+otro.ID, "carla", ""); w.Code != 403 {
			t.Fatalf("got %d", w.Code)
		}
	})

	t.Run("dados dos planes de Ana y uno cuidado por Carla, cuando cada una lista, entonces Ana ve sus 2 y Carla supervisa 1", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", p.ID, "carla")
		for _, q := range []struct {
			tok, url string
			n        int
		}{{"ana", "/api/v1/plans", 2}, {"carla", "/api/v1/plans", 0}, {"carla", "/api/v1/plans/supervised", 1}} {
			w := do(h, "GET", q.url, q.tok, "")
			var list []planJSON
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list) != q.n {
				t.Errorf("%s %s: esperaba %d, got %d %s", q.tok, q.url, q.n, w.Code, w.Body.String())
			}
		}
	})
}

func TestModifyPlansHTTP(t *testing.T) {
	type op struct {
		method, path, body string
		okCode             int
	}
	ops := func(p planJSON) []op {
		base := "/api/v1/plans/" + p.ID
		return []op{
			{"PATCH", base, `{"name":"Otro nombre"}`, 200},
			{"PUT", base + "/medications/" + p.Medications[0].ID, medBody, 200},
			{"PATCH", base + "/medications/" + p.Medications[0].ID, `{"dose":"15 mg"}`, 200},
			{"DELETE", base + "/medications/" + p.Medications[0].ID, "", 200},
			{"DELETE", base, "", 204},
		}
	}
	scenarios := []struct {
		quien, owner, actor string
		conCuidadora        bool
		permitido           bool
	}{
		{"dueño de un plan sin cuidador", "lucia", "lucia", false, true},
		{"dueño de un plan con cuidadora", "ana", "ana", true, false},
		{"cuidadora de ese plan", "ana", "carla", true, true},
		{"usuario sin vínculo con el plan", "ana", "dario", true, false},
	}
	for _, sc := range scenarios {
		for i := 0; i < 5; i++ {
			t.Run(fmt.Sprintf("dado un %s, cuando intenta la acción %d de edición/borrado, entonces responde según permisos", sc.quien, i), func(t *testing.T) {
				h := givenAPI()
				p := givenPlanOf(t, h, sc.owner)
				if sc.conCuidadora {
					givenCaregiver(t, h, sc.owner, p.ID, "carla")
				}
				o := ops(p)[i]
				want := 403
				if sc.permitido {
					want = o.okCode
				}
				if w := do(h, o.method, o.path, sc.actor, o.body); w.Code != want {
					t.Fatalf("%s %s: esperaba %d, got %d (%s)", o.method, o.path, want, w.Code, w.Body.String())
				}
			})
		}
	}

	t.Run("dado un plan de Ana con cuidadora, cuando Ana agrega un medicamento, entonces 201 (crear está permitido); Darío 403", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", p.ID, "carla")
		path := "/api/v1/plans/" + p.ID + "/medications"
		for tok, want := range map[string]int{"ana": 201, "carla": 201, "dario": 403} {
			if w := do(h, "POST", path, tok, medBody); w.Code != want {
				t.Errorf("%s: esperaba %d, got %d", tok, want, w.Code)
			}
		}
	})

	t.Run("dado un plan eliminado, cuando se consulta, entonces 404", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "lucia")
		do(h, "DELETE", "/api/v1/plans/"+p.ID, "lucia", "")
		if w := do(h, "GET", "/api/v1/plans/"+p.ID, "lucia", ""); w.Code != 404 {
			t.Fatalf("got %d", w.Code)
		}
	})

	t.Run("dado un plan con un solo medicamento, cuando se elimina ese medicamento, entonces 422", func(t *testing.T) {
		h := givenAPI()
		one := `{"name":"P","medications":[{"name":"Enalapril","dose":"10 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":1,"timeOfDay":"08:00"}]}]}`
		p := decodePlan(t, do(h, "POST", "/api/v1/plans", "lucia", one))
		if w := do(h, "DELETE", "/api/v1/plans/"+p.ID+"/medications/"+p.Medications[0].ID, "lucia", ""); w.Code != 422 {
			t.Fatalf("got %d", w.Code)
		}
	})
}

func TestPatchMedicationHTTP(t *testing.T) {
	patch := func(h http.Handler, user string, p planJSON, body string) *httptest.ResponseRecorder {
		return do(h, "PATCH", "/api/v1/plans/"+p.ID+"/medications/"+p.Medications[0].ID, user, body)
	}

	t.Run("dado un medicamento, cuando se manda solo la dosis, entonces responde 200 con el plan y lo demás intacto", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "lucia")
		w := patch(h, "lucia", p, `{"dose":"20 mg"}`)
		var got struct {
			Name        string
			Medications []struct {
				Name, Dose   string
				StockUnits   int
				StockAlertAt int
				Schedules    []struct{ TimeOfDay string }
			}
		}
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		m := got.Medications[0]
		if w.Code != 200 || m.Dose != "20 mg" || m.Name != "Enalapril" || m.StockUnits != 30 || m.StockAlertAt != 5 || len(m.Schedules) != 1 || got.Name != "Hipertensión" {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	})

	t.Run("dado un medicamento con fecha de fin, cuando se manda endDate null, entonces se borra; con una fecha, se fija", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "lucia")
		if w := patch(h, "lucia", p, `{"endDate":"2026-12-31"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"endDate":"2026-12-31"`) {
			t.Fatalf("fijar: %d %s", w.Code, w.Body.String())
		}
		if w := patch(h, "lucia", p, `{"endDate":null}`); w.Code != 200 || strings.Contains(w.Body.String(), "endDate") {
			t.Fatalf("borrar: %d %s", w.Code, w.Body.String())
		}
	})

	t.Run("dado un parche con ajuste de stock y de horarios, cuando se manda, entonces se aplican ambos", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "lucia")
		w := patch(h, "lucia", p, `{"stockUnits":12,"schedules":[{"dayOfWeek":6,"timeOfDay":"07:15"}]}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"stockUnits":12`) || !strings.Contains(w.Body.String(), `"07:15"`) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	})

	cases := []struct {
		name, body string
		want       int
	}{
		{"dado un JSON roto, cuando se edita parcialmente, entonces 400", `{`, 400},
		{"dado un campo desconocido (por ejemplo un error de tipeo), cuando se edita, entonces 400 y no se ignora en silencio", `{"stock":5}`, 400},
		{"dada una fecha mal formada, cuando se edita, entonces 400", `{"startDate":"01/11/2026"}`, 400},
		{"dado un tipo equivocado, cuando se edita, entonces 400", `{"stockUnits":"muchas"}`, 400},
		{"dado un cuerpo vacío, cuando se edita, entonces 422 (no hay nada para cambiar)", `{}`, 422},
		{"dada una dosis vacía, cuando se edita, entonces 422", `{"dose":""}`, 422},
		{"dado un stock negativo, cuando se edita, entonces 422", `{"stockUnits":-3}`, 422},
		{"dada una fecha de fin anterior al inicio, cuando se edita, entonces 422", `{"endDate":"2020-01-01"}`, 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := givenAPI()
			p := givenPlanOf(t, h, "lucia")
			if w := patch(h, "lucia", p, c.body); w.Code != c.want {
				t.Fatalf("esperaba %d, got %d (%s)", c.want, w.Code, w.Body.String())
			}
		})
	}

	t.Run("dado un medicamento inexistente o una sesión ausente, cuando se edita, entonces 404 y 401", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "lucia")
		if w := do(h, "PATCH", "/api/v1/plans/"+p.ID+"/medications/nope", "lucia", `{"dose":"1 mg"}`); w.Code != 404 {
			t.Errorf("inexistente: %d", w.Code)
		}
		if w := patch(h, "", p, `{"dose":"1 mg"}`); w.Code != 401 {
			t.Errorf("sin sesión: %d", w.Code)
		}
	})
}
