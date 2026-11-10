package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

type inviteJSON struct {
	ID          string `json:"id"`
	PlanID      string `json:"planId"`
	Email       string `json:"email"`
	CaregiverID string `json:"caregiverId"`
	Status      string `json:"status"`
	Token       string `json:"token"`
}

// invite hace que `owner` invite a <invitee>@mail.com a su plan y devuelve la respuesta.
func invite(h http.Handler, owner, planID, email string) (int, inviteJSON) {
	w := do(h, "POST", "/api/v1/plans/"+planID+"/caregivers", owner, `{"email":"`+email+`"}`)
	var inv inviteJSON
	_ = json.Unmarshal(w.Body.Bytes(), &inv)
	return w.Code, inv
}

func TestInviteHTTP(t *testing.T) {
	t.Run("dado un plan de Ana, cuando Ana invita a carla@mail.com, entonces 201 con la invitación pendiente y el token del enlace", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		code, inv := invite(h, "ana", p.ID, "carla@mail.com")
		if code != 201 || inv.Status != "pending" || inv.Email != "carla@mail.com" || inv.Token == "" || inv.CaregiverID != "" {
			t.Fatalf("got %d %+v", code, inv)
		}
	})

	cases := []struct {
		name  string
		setup func(h http.Handler, planID string)
		actor string
		email string
		want  int
	}{
		{"dado que no hay sesión, cuando invita, entonces 401", nil, "", "carla@mail.com", 401},
		{"dado un usuario que no es dueño, cuando invita, entonces 403", nil, "dario", "carla@mail.com", 403},
		{"dado un email inválido, cuando Ana invita, entonces 422", nil, "ana", "no-es-email", 422},
		{"dado un email ya invitado, cuando Ana invita de nuevo, entonces 409", func(h http.Handler, id string) { invite(h, "ana", id, "carla@mail.com") }, "ana", "carla@mail.com", 409},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := givenAPI()
			p := givenPlanOf(t, h, "ana")
			if c.setup != nil {
				c.setup(h, p.ID)
			}
			if code, _ := invite(h, c.actor, p.ID, c.email); code != c.want {
				t.Fatalf("esperaba %d, got %d", c.want, code)
			}
		})
	}

	t.Run("dado un plan inexistente, cuando Ana invita, entonces 404", func(t *testing.T) {
		if code, _ := invite(givenAPI(), "ana", "nope", "carla@mail.com"); code != 404 {
			t.Fatalf("got %d", code)
		}
	})
}

func TestAcceptByLinkHTTP(t *testing.T) {
	t.Run("dado un enlace para Carla, cuando Carla acepta, entonces 200 y pasa a ser cuidadora de ese plan", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		_, inv := invite(h, "ana", p.ID, "carla@mail.com")
		w := do(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"`+inv.Token+`"}`)
		var got inviteJSON
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if w.Code != 200 || got.Status != "accepted" || got.CaregiverID != "carla" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		if r := do(h, "GET", "/api/v1/plans/"+p.ID, "carla", ""); r.Code != 200 {
			t.Fatalf("Carla debería ver el plan, got %d", r.Code)
		}
	})

	t.Run("dado un enlace para Carla, cuando lo usa Darío, entonces 403 y Darío no ve el plan", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		_, inv := invite(h, "ana", p.ID, "carla@mail.com")
		if w := do(h, "POST", "/api/v1/invitations/accept", "dario", `{"token":"`+inv.Token+`"}`); w.Code != 403 {
			t.Fatalf("got %d", w.Code)
		}
		if r := do(h, "GET", "/api/v1/plans/"+p.ID, "dario", ""); r.Code != 403 {
			t.Fatalf("Darío no debería ver el plan, got %d", r.Code)
		}
	})

	t.Run("dado un email sin verificar, token inventado o sin sesión, cuando se acepta, entonces 403, 404 y 401", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		_, inv := invite(h, "ana", p.ID, "eva@mail.com")
		if w := do(h, "POST", "/api/v1/invitations/accept", "eva", `{"token":"`+inv.Token+`"}`); w.Code != 403 {
			t.Errorf("sin verificar: got %d", w.Code)
		}
		if w := do(h, "POST", "/api/v1/invitations/accept", "carla", `{"token":"inventado"}`); w.Code != 404 {
			t.Errorf("inventado: got %d", w.Code)
		}
		if w := do(h, "POST", "/api/v1/invitations/accept", "", `{"token":"x"}`); w.Code != 401 {
			t.Errorf("sin sesión: got %d", w.Code)
		}
	})
}

func TestPreviewHTTP(t *testing.T) {
	t.Run("dado un enlace válido, cuando se abre la vista previa SIN sesión, entonces 200 con el nombre del plan; con token inventado 404", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		_, inv := invite(h, "ana", p.ID, "carla@mail.com")
		w := do(h, "GET", "/api/v1/invitations/preview?token="+inv.Token, "", "")
		var pv struct{ PlanName string }
		_ = json.Unmarshal(w.Body.Bytes(), &pv)
		if w.Code != 200 || pv.PlanName != "Hipertensión" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		if w := do(h, "GET", "/api/v1/invitations/preview?token=inventado", "", ""); w.Code != 404 {
			t.Fatalf("inventado: got %d", w.Code)
		}
	})
}

func TestPendingInvitationsHTTP(t *testing.T) {
	t.Run("dadas invitaciones a carla@mail.com, cuando Carla las lista, acepta una y rechaza otra, entonces quedan resueltas", func(t *testing.T) {
		h := givenAPI()
		p1, p2 := givenPlanOf(t, h, "ana"), givenPlanOf(t, h, "ana")
		_, i1 := invite(h, "ana", p1.ID, "carla@mail.com")
		_, i2 := invite(h, "ana", p2.ID, "carla@mail.com")

		w := do(h, "GET", "/api/v1/invitations", "carla", "")
		var list []inviteJSON
		if _ = json.Unmarshal(w.Body.Bytes(), &list); w.Code != 200 || len(list) != 2 {
			t.Fatalf("esperaba 2 pendientes, got %d %s", w.Code, w.Body.String())
		}
		if w := do(h, "POST", "/api/v1/invitations/"+i1.ID+"/accept", "carla", ""); w.Code != 200 {
			t.Errorf("aceptar: got %d", w.Code)
		}
		if w := do(h, "POST", "/api/v1/invitations/"+i2.ID+"/reject", "carla", ""); w.Code != 204 {
			t.Errorf("rechazar: got %d", w.Code)
		}
		if r := do(h, "GET", "/api/v1/plans/"+p2.ID, "carla", ""); r.Code != 403 {
			t.Errorf("el plan rechazado no debería verse, got %d", r.Code)
		}
	})

	t.Run("dada una invitación de Carla, cuando Darío intenta aceptarla o rechazarla, entonces 403", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		_, inv := invite(h, "ana", p.ID, "carla@mail.com")
		if w := do(h, "POST", "/api/v1/invitations/"+inv.ID+"/accept", "dario", ""); w.Code != 403 {
			t.Errorf("aceptar: got %d", w.Code)
		}
		if w := do(h, "POST", "/api/v1/invitations/"+inv.ID+"/reject", "dario", ""); w.Code != 403 {
			t.Errorf("rechazar: got %d", w.Code)
		}
	})
}

func TestManageCaregiversHTTP(t *testing.T) {
	t.Run("dado un plan con Carla de cuidadora, cuando Ana y Carla lo consultan, entonces ven la lista; Darío 403", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", p.ID, "carla")
		for tok, want := range map[string]int{"ana": 200, "carla": 200, "dario": 403} {
			if w := do(h, "GET", "/api/v1/plans/"+p.ID+"/caregivers", tok, ""); w.Code != want {
				t.Errorf("%s: esperaba %d, got %d", tok, want, w.Code)
			}
		}
	})

	t.Run("dado un plan con cuidadora, cuando Ana la quita, entonces 204 y Carla pierde el acceso; Carla no puede quitarse a otro", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", p.ID, "carla")
		var list []inviteJSON
		_ = json.Unmarshal(do(h, "GET", "/api/v1/plans/"+p.ID+"/caregivers", "ana", "").Body.Bytes(), &list)
		path := "/api/v1/plans/" + p.ID + "/caregivers/" + list[0].ID
		if w := do(h, "DELETE", path, "carla", ""); w.Code != 403 {
			t.Errorf("Carla no es dueña: got %d", w.Code)
		}
		if w := do(h, "DELETE", path, "ana", ""); w.Code != 204 {
			t.Fatalf("quitar: got %d", w.Code)
		}
		if r := do(h, "GET", "/api/v1/plans/"+p.ID, "carla", ""); r.Code != 403 {
			t.Errorf("Carla debería haber perdido el acceso, got %d", r.Code)
		}
		// y Ana recupera la edición del plan al no tener cuidadora
		if w := do(h, "PATCH", "/api/v1/plans/"+p.ID, "ana", `{"name":"Mío otra vez"}`); w.Code != 200 {
			t.Errorf("Ana debería poder editar de nuevo, got %d", w.Code)
		}
	})

	t.Run("dado un plan con cuidadora, cuando Carla deja de seguirlo, entonces 204 y pierde el acceso; si no era cuidadora, 404", func(t *testing.T) {
		h := givenAPI()
		p := givenPlanOf(t, h, "ana")
		givenCaregiver(t, h, "ana", p.ID, "carla")
		if w := do(h, "DELETE", "/api/v1/plans/"+p.ID+"/follow", "carla", ""); w.Code != 204 {
			t.Fatalf("dejar de seguir: got %d", w.Code)
		}
		if r := do(h, "GET", "/api/v1/plans/"+p.ID, "carla", ""); r.Code != 403 {
			t.Errorf("debería haber perdido el acceso, got %d", r.Code)
		}
		if w := do(h, "DELETE", "/api/v1/plans/"+p.ID+"/follow", "dario", ""); w.Code != 404 {
			t.Errorf("Darío: esperaba 404, got %d", w.Code)
		}
	})
}
