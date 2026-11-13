package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/adapter/postgres/postgrestest"
	"github.com/lea/mediplan-api/internal/usecase/auth"
	"github.com/lea/mediplan-api/internal/usecase/notifications"
)

func TestMain(m *testing.M) { os.Exit(postgrestest.Run(m)) }

// fakeVerifier hace de Firebase: el token de sesión es "<nombre>" y su identidad <nombre>@mail.com.
type fakeVerifier struct{}

func (fakeVerifier) Verify(_ context.Context, tok string) (*auth.Identity, error) {
	if tok == "" || tok == "malo" {
		return nil, errors.New("token inválido")
	}
	return &auth.Identity{UID: "fb-" + tok, Email: tok + "@mail.com", EmailVerified: true}, nil
}

func call(h http.Handler, method, path, token, body string) (int, []byte) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func field(t *testing.T, raw []byte, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("no es JSON: %s", raw)
	}
	s, _ := m[key].(string)
	return s
}

const plan = `{"name":"Hipertensión","medications":[
 {"name":"Enalapril","dose":"10 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":1,"timeOfDay":"08:00"}],"stockUnits":30,"stockAlertAt":5},
 {"name":"Aspirina","dose":"100 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":2,"timeOfDay":"09:00"}]}]}`

func TestEndToEnd(t *testing.T) {
	t.Run("dada la API real sobre Postgres, cuando Ana crea un plan e invita a Carla y ella acepta, entonces los permisos por plan se cumplen de punta a punta", func(t *testing.T) {
		// Given: la aplicación completa con una base vacía
		h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}})

		// When/Then: Ana y Carla se registran (Firebase ya verificó sus emails)
		for _, u := range []string{"ana", "carla", "dario"} {
			if code, body := call(h, "POST", "/api/v1/auth/register", u, `{"role":"patient"}`); code != 200 {
				t.Fatalf("registro de %s: %d %s", u, code, body)
			}
		}
		// registrar dos veces no duplica ni cambia nada
		if code, _ := call(h, "POST", "/api/v1/auth/register", "ana", `{"role":"caregiver"}`); code != 200 {
			t.Fatalf("registro repetido: %d", code)
		}

		// Ana crea su plan y puede editarlo (todavía no tiene cuidadora)
		code, body := call(h, "POST", "/api/v1/plans", "ana", plan)
		if code != 201 {
			t.Fatalf("crear plan: %d %s", code, body)
		}
		planID := field(t, body, "id")
		if code, _ := call(h, "PATCH", "/api/v1/plans/"+planID, "ana", `{"name":"Mi plan"}`); code != 200 {
			t.Fatalf("Ana debería poder editar sin cuidadora: %d", code)
		}

		// Ana invita a Carla; Darío no puede ver nada ni usar el enlace
		code, body = call(h, "POST", "/api/v1/plans/"+planID+"/caregivers", "ana", `{"email":"carla@mail.com"}`)
		if code != 201 {
			t.Fatalf("invitar: %d %s", code, body)
		}
		tok := field(t, body, "token")
		if code, _ := call(h, "GET", "/api/v1/invitations/preview?token="+tok, "", ""); code != 200 {
			t.Fatalf("vista previa pública: %d", code)
		}
		if code, _ := call(h, "POST", "/api/v1/invitations/accept", "dario", `{"token":"`+tok+`"}`); code != 403 {
			t.Fatalf("Darío no debería poder usar el enlace: %d", code)
		}
		if code, _ := call(h, "GET", "/api/v1/plans/"+planID, "carla", ""); code != 403 {
			t.Fatalf("Carla no ve el plan antes de aceptar: %d", code)
		}

		// Carla acepta y pasa a ver el plan, que aparece entre los que supervisa
		if code, body := call(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"`+tok+`"}`); code != 200 {
			t.Fatalf("aceptar: %d %s", code, body)
		}
		if code, _ := call(h, "GET", "/api/v1/plans/"+planID, "carla", ""); code != 200 {
			t.Fatalf("Carla debería ver el plan: %d", code)
		}
		if _, body := call(h, "GET", "/api/v1/plans/supervised", "carla", ""); !strings.Contains(string(body), planID) {
			t.Fatalf("el plan debería figurar como supervisado: %s", body)
		}

		// Con cuidadora, Ana ya no edita pero sí agrega; Carla edita
		if code, _ := call(h, "PATCH", "/api/v1/plans/"+planID, "ana", `{"name":"x"}`); code != 403 {
			t.Fatalf("Ana ya no debería editar: %d", code)
		}
		med := `{"name":"Atorvastatina","dose":"20 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":3,"timeOfDay":"21:00"}]}`
		if code, _ := call(h, "POST", "/api/v1/plans/"+planID+"/medications", "ana", med); code != 201 {
			t.Fatalf("Ana sí puede agregar medicamentos: %d", code)
		}
		if code, _ := call(h, "PATCH", "/api/v1/plans/"+planID, "carla", `{"name":"Editado por Carla"}`); code != 200 {
			t.Fatalf("Carla debería poder editar: %d", code)
		}
		_, body = call(h, "GET", "/api/v1/plans/"+planID, "ana", "")
		if field(t, body, "name") != "Editado por Carla" || strings.Count(string(body), `"dose"`) != 3 {
			t.Fatalf("estado final inesperado: %s", body)
		}

		// Carla deja de seguir: pierde el acceso y Ana recupera la edición
		if code, _ := call(h, "DELETE", "/api/v1/plans/"+planID+"/follow", "carla", ""); code != 204 {
			t.Fatalf("dejar de seguir: %d", code)
		}
		if code, _ := call(h, "GET", "/api/v1/plans/"+planID, "carla", ""); code != 403 {
			t.Fatalf("Carla pierde el acceso: %d", code)
		}
		if code, _ := call(h, "PATCH", "/api/v1/plans/"+planID, "ana", `{"name":"Otra vez mío"}`); code != 200 {
			t.Fatalf("Ana recupera la edición: %d", code)
		}
	})

	t.Run("dada la API real, cuando no hay sesión o el token es malo, entonces 401; y un usuario sin registrar recibe 403", func(t *testing.T) {
		h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}})
		if code, _ := call(h, "GET", "/api/v1/plans", "", ""); code != 401 {
			t.Errorf("sin sesión: %d", code)
		}
		if code, _ := call(h, "GET", "/api/v1/plans", "malo", ""); code != 401 {
			t.Errorf("token malo: %d", code)
		}
		if code, _ := call(h, "GET", "/api/v1/plans", "eva", ""); code != 403 {
			t.Errorf("sin registrar: %d", code)
		}
	})
}

// medDiaria: toma todos los días a las 00:00 hora local. Hoy a las 00:00 local ya pasó (y no es futuro),
// así que el test funciona a cualquier hora en que se corra.
const planDiario = `{"name":"Diario","medications":[{"name":"Enalapril","dose":"10 mg","startDate":"2026-01-01",
 "schedules":[{"dayOfWeek":0,"timeOfDay":"00:00"},{"dayOfWeek":1,"timeOfDay":"00:00"},{"dayOfWeek":2,"timeOfDay":"00:00"},
 {"dayOfWeek":3,"timeOfDay":"00:00"},{"dayOfWeek":4,"timeOfDay":"00:00"},{"dayOfWeek":5,"timeOfDay":"00:00"},{"dayOfWeek":6,"timeOfDay":"00:00"}]}]}`

func TestDosesEndToEnd(t *testing.T) {
	t.Run("dada la API real, cuando Ana registra una toma, entonces es idempotente, la ve su cuidadora y se validan las horas", func(t *testing.T) {
		h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}})
		for _, u := range []string{"ana", "carla", "dario"} {
			call(h, "POST", "/api/v1/auth/register", u, `{"role":"patient"}`)
		}
		if code, body := call(h, "PATCH", "/api/v1/me", "ana", `{"timezone":"America/Argentina/Buenos_Aires"}`); code != 200 {
			t.Fatalf("zona horaria: %d %s", code, body)
		}
		_, body := call(h, "POST", "/api/v1/plans", "ana", planDiario)
		planID := field(t, body, "id")
		var created struct {
			Medications []struct{ ID string } `json:"medications"`
		}
		_ = json.Unmarshal(body, &created)
		medID := created.Medications[0].ID
		_, inv := call(h, "POST", "/api/v1/plans/"+planID+"/caregivers", "ana", `{"email":"carla@mail.com"}`)
		call(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"`+field(t, inv, "token")+`"}`)

		// hoy a las 00:00 hora argentina (UTC-3): ya pasó
		ar := time.FixedZone("ART", -3*3600)
		hoy := time.Now().In(ar)
		medianoche := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, ar).UTC().Format(time.RFC3339)
		path := "/api/v1/plans/" + planID + "/medications/" + medID + "/doses"

		code, first := call(h, "POST", path, "ana", `{"scheduledAt":"`+medianoche+`"}`)
		if code != 200 || field(t, first, "status") != "taken" {
			t.Fatalf("registrar: %d %s", code, first)
		}
		code, second := call(h, "POST", path, "carla", `{"scheduledAt":"`+medianoche+`"}`)
		if code != 200 || field(t, second, "id") != field(t, first, "id") {
			t.Fatalf("idempotente: %d %s", code, second)
		}

		if code, _ := call(h, "POST", path, "dario", `{"scheduledAt":"`+medianoche+`"}`); code != 403 {
			t.Errorf("sin vínculo: %d", code)
		}
		if code, _ := call(h, "POST", path, "ana", `{"scheduledAt":"2026-01-05T03:05:00Z"}`); code != 422 {
			t.Errorf("hora que no corresponde: %d", code)
		}
		if code, _ := call(h, "POST", path, "ana", `{"scheduledAt":"ayer"}`); code != 400 {
			t.Errorf("fecha mal formada: %d", code)
		}
		if code, _ := call(h, "POST", path, "ana", `{`); code != 400 {
			t.Errorf("json roto: %d", code)
		}

		// historial y estadísticas: forma de la respuesta, permisos y rango
		if code, body := call(h, "GET", "/api/v1/plans/"+planID+"/doses", "carla", ""); code != 200 || !strings.HasPrefix(string(body), "[") {
			t.Errorf("historial: %d %s", code, body)
		}
		code, st := call(h, "GET", "/api/v1/plans/"+planID+"/stats", "carla", "")
		var stats map[string]any
		_ = json.Unmarshal(st, &stats)
		if code != 200 || stats["expected"] == nil || stats["taken"] == nil || stats["missed"] == nil || stats["pending"] == nil {
			t.Errorf("estadísticas: %d %s", code, st)
		}
		if code, _ := call(h, "GET", "/api/v1/plans/"+planID+"/stats", "dario", ""); code != 403 {
			t.Errorf("stats sin vínculo: %d", code)
		}
		if code, _ := call(h, "GET", "/api/v1/plans/"+planID+"/doses?from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z", "ana", ""); code != 422 {
			t.Errorf("rango enorme: %d", code)
		}
		if code, _ := call(h, "GET", "/api/v1/plans/"+planID+"/doses?from=hoy", "ana", ""); code != 400 {
			t.Errorf("fecha inválida: %d", code)
		}
	})
}

// ---- Avisos de dosis omitida y sincronización ----

type recordingPusher struct{ sent []pushed }
type pushed struct {
	tokens []string
	msg    notifications.PushMessage
}

func (r *recordingPusher) Send(_ context.Context, tokens []string, msg notifications.PushMessage) ([]string, error) {
	r.sent = append(r.sent, pushed{tokens, msg})
	return nil, nil
}

func (r *recordingPusher) to(token string) (out []notifications.PushMessage) {
	for _, p := range r.sent {
		for _, t := range p.tokens {
			if t == token {
				out = append(out, p.msg)
			}
		}
	}
	return out
}

// planHaceDosHoras: toma todos los días a la hora local de hace 2 horas, así que HOY siempre hay
// exactamente una toma vencida (hace 2 h: pasó el margen de 30 min y está dentro de las últimas 24 h).
func planHaceDosHoras() string {
	ar := time.FixedZone("ART", -3*3600)
	hhmm := time.Now().In(ar).Add(-2 * time.Hour).Format("15:04")
	sched := ""
	for d := 0; d < 7; d++ {
		if d > 0 {
			sched += ","
		}
		sched += `{"dayOfWeek":` + string(rune('0'+d)) + `,"timeOfDay":"` + hhmm + `"}`
	}
	return `{"name":"Hipertensión","medications":[{"name":"Enalapril","dose":"10 mg","startDate":"2026-01-01","schedules":[` + sched + `]}]}`
}

func withJobHeader(h http.Handler, secret string) (int, []byte) {
	req := httptest.NewRequest("POST", "/internal/jobs/missed-doses", nil)
	if secret != "" {
		req.Header.Set("X-Job-Secret", secret)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func TestMissedDoseAlertEndToEnd(t *testing.T) {
	t.Run("dada una toma vencida sin registrar, cuando corre el disparador del Cloud Scheduler, entonces la cuidadora y la paciente reciben cada una su aviso una sola vez", func(t *testing.T) {
		pool := postgrestest.Setup(t)
		pusher := &recordingPusher{}
		h := NewHandler(Deps{Pool: pool, Verifier: fakeVerifier{}, Pusher: pusher, JobSecret: "s3creto"})

		for _, u := range []string{"ana", "carla"} {
			call(h, "POST", "/api/v1/auth/register", u, `{"role":"patient"}`)
		}
		call(h, "PATCH", "/api/v1/me", "ana", `{"timezone":"America/Argentina/Buenos_Aires"}`)
		if code, body := call(h, "POST", "/api/v1/devices", "carla", `{"token":"carla-phone","platform":"android"}`); code != 204 {
			t.Fatalf("registrar celular: %d %s", code, body)
		}
		_, body := call(h, "POST", "/api/v1/plans", "ana", planHaceDosHoras())
		planID := field(t, body, "id")
		_, inv := call(h, "POST", "/api/v1/plans/"+planID+"/caregivers", "ana", `{"email":"carla@mail.com"}`)
		call(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"`+field(t, inv, "token")+`"}`)
		// el plan "existe desde hace días": si no, la toma de hace 2 horas sería anterior a su creación
		if _, err := pool.Exec(context.Background(), "UPDATE treatment_plans SET created_at = now() - interval '3 days'"); err != nil {
			t.Fatal(err)
		}

		if code, _ := withJobHeader(h, ""); code != 401 {
			t.Errorf("sin secreto: %d", code)
		}
		if code, _ := withJobHeader(h, "otro"); code != 401 {
			t.Errorf("secreto incorrecto: %d", code)
		}
		code, report := withJobHeader(h, "s3creto")
		var rep struct{ NewMissed, Notified, Failed int }
		_ = json.Unmarshal(report, &rep)
		if code != 200 || rep.NewMissed != 1 || rep.Notified != 2 || rep.Failed != 0 {
			t.Fatalf("disparador: %d %s", code, report)
		}

		// Carla ve el aviso en la app y le llegó el push a su celular
		_, list := call(h, "GET", "/api/v1/notifications", "carla", "")
		var avisos []struct {
			ID, Kind, Title, Body string
			ReadAt                *time.Time
			Data                  map[string]string
		}
		_ = json.Unmarshal(list, &avisos)
		if len(avisos) != 1 || avisos[0].Kind != "missed_dose" || !strings.Contains(avisos[0].Body, "ana@mail.com") || avisos[0].Data["planId"] != planID || avisos[0].ReadAt != nil {
			t.Fatalf("avisos: %s", list)
		}
		visibles := 0
		for _, m := range pusher.to("carla-phone") {
			if !m.Silent && m.Title == "Dosis no registrada" {
				visibles++
			}
		}
		if visibles != 1 {
			t.Fatalf("esperaba 1 push visible a Carla, got %d (%+v)", visibles, pusher.sent)
		}

		// Ana ve SU aviso (en primera persona, sin nombrarse a sí misma), no el de Carla
		_, deAna := call(h, "GET", "/api/v1/notifications", "ana", "")
		var avisosAna []struct {
			ID, Kind, Body string
			Data           map[string]string
		}
		_ = json.Unmarshal(deAna, &avisosAna)
		if len(avisosAna) != 1 || avisosAna[0].Kind != "missed_dose" || !strings.Contains(avisosAna[0].Body, "No registraste") || strings.Contains(avisosAna[0].Body, "ana@mail.com") || avisosAna[0].Data["planId"] != planID || avisosAna[0].ID == avisos[0].ID {
			t.Fatalf("avisos de Ana: %s", deAna)
		}

		// correr de nuevo no repite el aviso; Ana no puede leer el aviso de Carla; Carla lo marca como leído
		if _, rep2 := withJobHeader(h, "s3creto"); !strings.Contains(string(rep2), `"newMissed":0`) {
			t.Errorf("segunda corrida: %s", rep2)
		}
		if code, _ := call(h, "POST", "/api/v1/notifications/"+avisos[0].ID+"/read", "ana", ""); code != 404 {
			t.Errorf("leído ajeno: %d", code)
		}
		if code, _ := call(h, "POST", "/api/v1/notifications/"+avisos[0].ID+"/read", "carla", ""); code != 204 {
			t.Errorf("leído propio: %d", code)
		}

		// si Ana registra esa toma tarde, queda como tomada (la omisión se corrige)
		_, hist := call(h, "GET", "/api/v1/plans/"+planID+"/doses", "ana", "")
		var ocurrencias []struct{ MedicationID, ScheduledAt, Status string }
		_ = json.Unmarshal(hist, &ocurrencias)
		if len(ocurrencias) == 0 || ocurrencias[len(ocurrencias)-1].Status != "missed" {
			t.Fatalf("el historial debería mostrar la omitida: %s", hist)
		}
		o := ocurrencias[len(ocurrencias)-1]
		path := "/api/v1/plans/" + planID + "/medications/" + o.MedicationID + "/doses"
		if code, b := call(h, "POST", path, "ana", `{"scheduledAt":"`+o.ScheduledAt+`"}`); code != 200 || field(t, b, "status") != "taken" {
			t.Fatalf("registro tardío: %d %s", code, b)
		}
	})

	t.Run("dado un plan con cuidadora que tiene celular, cuando Ana agrega un medicamento, entonces a Carla le llega un push silencioso de sincronización", func(t *testing.T) {
		pusher := &recordingPusher{}
		h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}, Pusher: pusher})
		for _, u := range []string{"ana", "carla"} {
			call(h, "POST", "/api/v1/auth/register", u, `{"role":"patient"}`)
		}
		call(h, "POST", "/api/v1/devices", "carla", `{"token":"carla-phone"}`)
		_, body := call(h, "POST", "/api/v1/plans", "ana", plan)
		planID := field(t, body, "id")
		_, inv := call(h, "POST", "/api/v1/plans/"+planID+"/caregivers", "ana", `{"email":"carla@mail.com"}`)
		call(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"`+field(t, inv, "token")+`"}`)

		pusher.sent = nil
		med := `{"name":"Atorvastatina","dose":"20 mg","startDate":"2026-11-01","schedules":[{"dayOfWeek":3,"timeOfDay":"21:00"}]}`
		if code, _ := call(h, "POST", "/api/v1/plans/"+planID+"/medications", "ana", med); code != 201 {
			t.Fatalf("agregar: %d", code)
		}
		msgs := pusher.to("carla-phone")
		if len(msgs) != 1 || !msgs[0].Silent || msgs[0].Data["kind"] != "plan_sync" || msgs[0].Data["planId"] != planID {
			t.Fatalf("Carla debería recibir un push silencioso de sincronización: %+v", msgs)
		}
		// y no aparece como aviso visible en su lista
		if _, list := call(h, "GET", "/api/v1/notifications", "carla", ""); strings.TrimSpace(string(list)) != "[]" {
			t.Fatalf("la sincronización no debe listarse: %s", list)
		}
	})

	t.Run("dado un servidor sin secreto de tareas configurado, cuando alguien llama al disparador, entonces no existe (404)", func(t *testing.T) {
		h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}})
		if code, _ := withJobHeader(h, "cualquiera"); code != 404 {
			t.Fatalf("got %d", code)
		}
	})

	t.Run("dado un celular, cuando el usuario lo da de baja o manda un token vacío, entonces 204 y 422", func(t *testing.T) {
		h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}})
		call(h, "POST", "/api/v1/auth/register", "carla", `{"role":"patient"}`)
		if code, _ := call(h, "POST", "/api/v1/devices", "carla", `{"token":"  "}`); code != 422 {
			t.Errorf("vacío: %d", code)
		}
		call(h, "POST", "/api/v1/devices", "carla", `{"token":"t1"}`)
		if code, _ := call(h, "DELETE", "/api/v1/devices", "carla", `{"token":"t1"}`); code != 204 {
			t.Errorf("baja: %d", code)
		}
		if code, _ := call(h, "POST", "/api/v1/devices", "", `{"token":"t1"}`); code != 401 {
			t.Errorf("sin sesión: %d", code)
		}
	})
}

// ---- Stock ----

// planConStock: toma diaria a las 00:00 hora argentina, con stock y umbral de aviso a elección.
func planConStock(units, alertAt int) string {
	sched := ""
	for d := 0; d < 7; d++ {
		if d > 0 {
			sched += ","
		}
		sched += `{"dayOfWeek":` + string(rune('0'+d)) + `,"timeOfDay":"00:00"}`
	}
	return fmt.Sprintf(`{"name":"Hipertensión","medications":[{"name":"Enalapril","dose":"10 mg","startDate":"2026-01-01",
 "schedules":[%s],"stockUnits":%d,"stockAlertAt":%d}]}`, sched, units, alertAt)
}

// medianoches devuelve las 00:00 (hora argentina) de hoy y de ayer, como instantes UTC en RFC 3339.
func medianoches() (hoy, ayer string) {
	ar := time.FixedZone("ART", -3*3600)
	n := time.Now().In(ar)
	h := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, ar)
	return h.UTC().Format(time.RFC3339), h.AddDate(0, 0, -1).UTC().Format(time.RFC3339)
}

type stockFixture struct {
	h      http.Handler
	planID string
	medID  string
}

func givenPlanWithStock(t *testing.T, units, alertAt int) stockFixture {
	t.Helper()
	h := NewHandler(Deps{Pool: postgrestest.Setup(t), Verifier: fakeVerifier{}})
	for _, u := range []string{"ana", "carla", "dario"} {
		call(h, "POST", "/api/v1/auth/register", u, `{"role":"patient"}`)
	}
	call(h, "PATCH", "/api/v1/me", "ana", `{"timezone":"America/Argentina/Buenos_Aires"}`)
	_, body := call(h, "POST", "/api/v1/plans", "ana", planConStock(units, alertAt))
	var plan struct {
		ID          string
		Medications []struct{ ID string }
	}
	_ = json.Unmarshal(body, &plan)
	_, inv := call(h, "POST", "/api/v1/plans/"+plan.ID+"/caregivers", "ana", `{"email":"carla@mail.com"}`)
	call(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"`+field(t, inv, "token")+`"}`)
	return stockFixture{h: h, planID: plan.ID, medID: plan.Medications[0].ID}
}

func (f stockFixture) take(user, scheduledAt string) int {
	code, _ := call(f.h, "POST", "/api/v1/plans/"+f.planID+"/medications/"+f.medID+"/doses", user, `{"scheduledAt":"`+scheduledAt+`"}`)
	return code
}

func (f stockFixture) stock(t *testing.T) int {
	t.Helper()
	_, body := call(f.h, "GET", "/api/v1/plans/"+f.planID, "ana", "")
	var p struct{ Medications []struct{ StockUnits int } }
	_ = json.Unmarshal(body, &p)
	return p.Medications[0].StockUnits
}

func (f stockFixture) avisos(t *testing.T, user string) []struct {
	Kind, Title, Body string
	Data              map[string]string
} {
	t.Helper()
	_, body := call(f.h, "GET", "/api/v1/notifications", user, "")
	var out []struct {
		Kind, Title, Body string
		Data              map[string]string
	}
	_ = json.Unmarshal(body, &out)
	return out
}

func TestStockEndToEnd(t *testing.T) {
	hoy, ayer := medianoches()

	t.Run("dado un stock de 6 con aviso en 5, cuando se toman dosis, entonces se descuenta, se avisa una sola vez al cruzar el umbral y repetir un registro no vuelve a descontar", func(t *testing.T) {
		f := givenPlanWithStock(t, 6, 5)

		if code := f.take("ana", hoy); code != 200 {
			t.Fatalf("registrar: %d", code)
		}
		if got := f.stock(t); got != 5 {
			t.Fatalf("stock tras la primera toma: %d", got)
		}
		for _, user := range []string{"ana", "carla"} { // el paciente y su cuidadora
			av := f.avisos(t, user)
			if len(av) != 1 || av[0].Kind != "low_stock" || av[0].Data["level"] != "low" || av[0].Data["remaining"] != "5" || !strings.Contains(av[0].Body, "Enalapril") {
				t.Fatalf("avisos de %s: %+v", user, av)
			}
		}
		if len(f.avisos(t, "dario")) != 0 {
			t.Error("Darío no debería recibir nada")
		}

		f.take("carla", ayer) // la cuidadora registra otra toma: 5 → 4, ya estaba por debajo
		if got := f.stock(t); got != 4 || len(f.avisos(t, "ana")) != 1 {
			t.Fatalf("stock=%d avisos de Ana=%d", got, len(f.avisos(t, "ana")))
		}
		f.take("ana", ayer) // repetida: idempotente
		if got := f.stock(t); got != 4 {
			t.Fatalf("una toma repetida no debe descontar: %d", got)
		}
	})

	t.Run("dado un stock de 1, cuando se toma la última unidad, entonces se avisa que se agotó", func(t *testing.T) {
		f := givenPlanWithStock(t, 1, 5)
		f.take("ana", hoy)
		av := f.avisos(t, "carla")
		if f.stock(t) != 0 || len(av) != 1 || av[0].Data["level"] != "out" || !strings.Contains(av[0].Body, "agotó") {
			t.Fatalf("stock=%d avisos=%+v", f.stock(t), av)
		}
	})

	t.Run("dado un medicamento con poco stock, cuando se repone, entonces suma; valida permisos y cantidad", func(t *testing.T) {
		f := givenPlanWithStock(t, 3, 5)
		path := "/api/v1/plans/" + f.planID + "/medications/" + f.medID + "/stock"

		code, body := call(f.h, "POST", path, "carla", `{"units":30}`)
		if code != 200 || field(t, body, "medicationId") != f.medID || f.stock(t) != 33 {
			t.Fatalf("reponer: %d %s stock=%d", code, body, f.stock(t))
		}
		if code, _ := call(f.h, "POST", path, "ana", `{"units":7}`); code != 200 || f.stock(t) != 40 {
			t.Errorf("el paciente también repone (aunque tenga cuidadora): %d stock=%d", code, f.stock(t))
		}
		if code, _ := call(f.h, "POST", path, "dario", `{"units":5}`); code != 403 {
			t.Errorf("sin vínculo: %d", code)
		}
		for body, want := range map[string]int{`{"units":0}`: 422, `{"units":-4}`: 422, `{"units":999999}`: 422, `{"units":"muchas"}`: 400, `{`: 400} {
			if code, _ := call(f.h, "POST", path, "ana", body); code != want {
				t.Errorf("%s: esperaba %d, got %d", body, want, code)
			}
		}
		if code, _ := call(f.h, "POST", "/api/v1/plans/"+f.planID+"/medications/00000000-0000-0000-0000-000000000000/stock", "ana", `{"units":5}`); code != 404 {
			t.Errorf("medicamento inexistente: %d", code)
		}
		if code, _ := call(f.h, "POST", path, "", `{"units":5}`); code != 401 {
			t.Errorf("sin sesión: %d", code)
		}
	})
}

func TestPatchKeepsStockEndToEnd(t *testing.T) {
	hoy, _ := medianoches()

	t.Run("dadas tomas que ya descontaron stock, cuando el cuidador edita solo la dosis con PATCH, entonces el stock descontado se conserva", func(t *testing.T) {
		f := givenPlanWithStock(t, 6, 5)
		f.take("ana", hoy) // 6 → 5
		path := "/api/v1/plans/" + f.planID + "/medications/" + f.medID

		if code, body := call(f.h, "PATCH", path, "carla", `{"dose":"20 mg"}`); code != 200 || !strings.Contains(string(body), `"dose":"20 mg"`) {
			t.Fatalf("patch: %d %s", code, body)
		}
		if got := f.stock(t); got != 5 {
			t.Fatalf("el stock debería seguir en 5, está en %d", got)
		}
		if code, _ := call(f.h, "PATCH", path, "carla", `{"stockUnits":40}`); code != 200 || f.stock(t) != 40 {
			t.Fatalf("ajuste explícito: %d stock=%d", code, f.stock(t))
		}
		if code, _ := call(f.h, "PATCH", path, "ana", `{"dose":"1 mg"}`); code != 403 {
			t.Errorf("la paciente con cuidadora no edita: %d", code)
		}
	})
}
