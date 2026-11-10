package caregivers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lea/mediplan-api/internal/domain"
)

// ---- fakes ----

type fakeInvites struct {
	rows map[string]*domain.PlanCaregiver
	seq  int
}

func (f *fakeInvites) Create(_ context.Context, c *domain.PlanCaregiver) error {
	f.seq++
	c.ID = fmt.Sprintf("inv%d", f.seq)
	cp := *c
	f.rows[c.ID] = &cp
	return nil
}
func (f *fakeInvites) get(match func(*domain.PlanCaregiver) bool) []*domain.PlanCaregiver {
	var out []*domain.PlanCaregiver
	for _, r := range f.rows {
		if match(r) {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out
}
func (f *fakeInvites) ByID(_ context.Context, id string) (*domain.PlanCaregiver, error) {
	if r, ok := f.rows[id]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakeInvites) ByTokenHash(_ context.Context, h string) (*domain.PlanCaregiver, error) {
	if r := f.get(func(c *domain.PlanCaregiver) bool { return c.TokenHash == h }); len(r) > 0 {
		return r[0], nil
	}
	return nil, domain.ErrNotFound
}
func (f *fakeInvites) ByPlan(_ context.Context, planID string) ([]*domain.PlanCaregiver, error) {
	return f.get(func(c *domain.PlanCaregiver) bool { return c.PlanID == planID }), nil
}
func (f *fakeInvites) PendingByEmail(_ context.Context, email string) ([]*domain.PlanCaregiver, error) {
	return f.get(func(c *domain.PlanCaregiver) bool {
		return c.Status == domain.InvitePending && c.InviteEmail == email
	}), nil
}
func (f *fakeInvites) Update(_ context.Context, c *domain.PlanCaregiver) error {
	cp := *c
	f.rows[c.ID] = &cp
	return nil
}
func (f *fakeInvites) Delete(_ context.Context, id string) error {
	delete(f.rows, id)
	return nil
}

type fakePlans map[string]*domain.Plan

func (f fakePlans) ByID(_ context.Context, id string) (*domain.Plan, error) {
	if p, ok := f[id]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}

// fakeTokens es determinista: el N-ésimo token es "tokN" y su hash "h:tokN".
type fakeTokens struct{ n int }

func (f *fakeTokens) New() (string, string, error) {
	f.n++
	raw := fmt.Sprintf("tok%d", f.n)
	return raw, f.Hash(raw), nil
}
func (f *fakeTokens) Hash(raw string) string { return "h:" + raw }

// ---- givens ----

func user(id string, verified bool) *domain.User {
	return &domain.User{ID: id, Email: id + "@mail.com", EmailVerified: verified}
}

var (
	ana        = user("ana", true) // dueña del plan p1
	carla      = user("carla", true)
	dario      = user("dario", true)
	sinVerific = user("eva", false)
	t0         = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
)

type world struct {
	svc     *Service
	invites *fakeInvites
	clock   *time.Time
}

func givenWorld() *world {
	now := t0
	w := &world{invites: &fakeInvites{rows: map[string]*domain.PlanCaregiver{}}, clock: &now}
	plans := fakePlans{
		"p1": {ID: "p1", PatientID: "ana", Name: "Hipertensión"},
		"p2": {ID: "p2", PatientID: "ana", Name: "Diabetes"},
	}
	w.svc = NewService(w.invites, plans, &fakeTokens{})
	w.svc.now = func() time.Time { return *w.clock }
	return w
}

// invitar crea una invitación de Ana a un email para el plan indicado y devuelve el token.
func (w *world) invitar(t *testing.T, planID, email string) (*domain.PlanCaregiver, string) {
	t.Helper()
	inv, tok, err := w.svc.Invite(context.Background(), ana, planID, email)
	if err != nil {
		t.Fatal(err)
	}
	return inv, tok
}

// aceptada deja a Carla como cuidadora aceptada del plan.
func (w *world) aceptada(t *testing.T, planID string) *domain.PlanCaregiver {
	t.Helper()
	_, tok := w.invitar(t, planID, "carla@mail.com")
	l, err := w.svc.Accept(context.Background(), carla, tok)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// ---- Invite ----

func TestInvite(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un plan de Ana, cuando Ana invita a un email, entonces queda pendiente, atada al email y vence en 7 días", func(t *testing.T) {
		w := givenWorld()
		inv, tok, err := w.svc.Invite(ctx, ana, "p1", "  Carla@Mail.com ")
		if err != nil {
			t.Fatal(err)
		}
		if inv.Status != domain.InvitePending || inv.InviteEmail != "carla@mail.com" || inv.PlanID != "p1" || inv.CaregiverID != "" {
			t.Fatalf("invitación inesperada: %+v", inv)
		}
		if tok == "" || inv.ExpiresAt != t0.Add(7*24*time.Hour) {
			t.Fatalf("token vacío o vencimiento incorrecto: %q %v", tok, inv.ExpiresAt)
		}
		if strings.Contains(inv.TokenHash, tok) && inv.TokenHash == tok {
			t.Fatal("no debe guardarse el token en claro")
		}
	})

	t.Run("dado un plan de Ana, cuando invita alguien que no es su dueño (ni siquiera su cuidadora), entonces no tiene permiso", func(t *testing.T) {
		w := givenWorld()
		w.aceptada(t, "p1")
		for _, u := range []*domain.User{carla, dario} {
			if _, _, err := w.svc.Invite(ctx, u, "p1", "otro@mail.com"); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s: esperaba ErrForbidden, got %v", u.ID, err)
			}
		}
	})

	t.Run("dado un email inválido o el propio email de Ana, cuando invita, entonces es rechazado", func(t *testing.T) {
		w := givenWorld()
		for _, email := range []string{"no-es-email", "", "ana@mail.com"} {
			if _, _, err := w.svc.Invite(ctx, ana, "p1", email); !errors.Is(err, domain.ErrInvalidEmail) {
				t.Errorf("%q: esperaba ErrInvalidEmail, got %v", email, err)
			}
		}
	})

	t.Run("dado un plan inexistente, cuando invita, entonces no se encuentra", func(t *testing.T) {
		w := givenWorld()
		if _, _, err := w.svc.Invite(ctx, ana, "nope", "carla@mail.com"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
	})

	t.Run("dado un email ya invitado (pendiente) o ya cuidador del plan, cuando se invita de nuevo, entonces es rechazado", func(t *testing.T) {
		w := givenWorld()
		w.invitar(t, "p1", "carla@mail.com")
		if _, _, err := w.svc.Invite(ctx, ana, "p1", "carla@mail.com"); !errors.Is(err, domain.ErrAlreadyInvited) {
			t.Errorf("pendiente: esperaba ErrAlreadyInvited, got %v", err)
		}
		w2 := givenWorld()
		w2.aceptada(t, "p1")
		if _, _, err := w2.svc.Invite(ctx, ana, "p1", "carla@mail.com"); !errors.Is(err, domain.ErrAlreadyInvited) {
			t.Errorf("aceptada: esperaba ErrAlreadyInvited, got %v", err)
		}
	})

	t.Run("dado un mismo email, cuando se lo invita a otro plan, entonces se permite (la supervisión es por plan)", func(t *testing.T) {
		w := givenWorld()
		w.invitar(t, "p1", "carla@mail.com")
		if _, _, err := w.svc.Invite(ctx, ana, "p2", "carla@mail.com"); err != nil {
			t.Fatalf("debería poder: %v", err)
		}
	})

	t.Run("dado una invitación rechazada, cuando Ana vuelve a invitar al mismo email, entonces se permite", func(t *testing.T) {
		w := givenWorld()
		inv, _ := w.invitar(t, "p1", "carla@mail.com")
		_ = w.svc.Reject(ctx, carla, inv.ID)
		if _, _, err := w.svc.Invite(ctx, ana, "p1", "carla@mail.com"); err != nil {
			t.Fatalf("debería poder: %v", err)
		}
	})
}

// ---- Accept (por enlace/token) ----

func TestAccept(t *testing.T) {
	ctx := context.Background()

	t.Run("dada una invitación para carla@mail.com, cuando Carla (email verificado) acepta con el token, entonces queda como cuidadora del plan", func(t *testing.T) {
		w := givenWorld()
		_, tok := w.invitar(t, "p1", "carla@mail.com")
		l, err := w.svc.Accept(ctx, carla, tok)
		if err != nil || l.Status != domain.InviteAccepted || l.CaregiverID != "carla" || l.PlanID != "p1" {
			t.Fatalf("got %+v err=%v", l, err)
		}
	})

	t.Run("dada una invitación para Carla, cuando otro usuario verificado usa el enlace, entonces no tiene permiso y sigue pendiente", func(t *testing.T) {
		w := givenWorld()
		inv, tok := w.invitar(t, "p1", "carla@mail.com")
		if _, err := w.svc.Accept(ctx, dario, tok); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("esperaba ErrForbidden, got %v", err)
		}
		if got, _ := w.invites.ByID(ctx, inv.ID); got.Status != domain.InvitePending || got.CaregiverID != "" {
			t.Fatalf("debería seguir pendiente: %+v", got)
		}
	})

	t.Run("dado un email sin verificar, cuando intenta aceptar, entonces es rechazado", func(t *testing.T) {
		w := givenWorld()
		_, tok := w.invitar(t, "p1", "eva@mail.com")
		if _, err := w.svc.Accept(ctx, sinVerific, tok); !errors.Is(err, domain.ErrEmailNotVerified) {
			t.Fatalf("esperaba ErrEmailNotVerified, got %v", err)
		}
	})

	t.Run("dada una invitación vencida, cuando Carla intenta aceptar, entonces es rechazada", func(t *testing.T) {
		w := givenWorld()
		_, tok := w.invitar(t, "p1", "carla@mail.com")
		*w.clock = t0.Add(8 * 24 * time.Hour)
		if _, err := w.svc.Accept(ctx, carla, tok); !errors.Is(err, domain.ErrInvitationExpired) {
			t.Fatalf("esperaba ErrInvitationExpired, got %v", err)
		}
	})

	t.Run("dado un token inventado o ya usado, cuando se acepta, entonces no se encuentra", func(t *testing.T) {
		w := givenWorld()
		_, tok := w.invitar(t, "p1", "carla@mail.com")
		_, _ = w.svc.Accept(ctx, carla, tok)
		for _, malo := range []string{"inventado", tok} {
			if _, err := w.svc.Accept(ctx, carla, malo); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("%q: esperaba ErrNotFound, got %v", malo, err)
			}
		}
	})
}

// ---- Invitaciones pendientes por email (sin enlace) ----

func TestPendingByEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("dadas dos invitaciones a carla@mail.com, cuando Carla lista sus pendientes y acepta una por id, entonces queda cuidadora de ese plan", func(t *testing.T) {
		w := givenWorld()
		w.invitar(t, "p1", "carla@mail.com")
		w.invitar(t, "p2", "carla@mail.com")
		lista, err := w.svc.ListPending(ctx, carla)
		if err != nil || len(lista) != 2 {
			t.Fatalf("esperaba 2 pendientes, got %d err=%v", len(lista), err)
		}
		l, err := w.svc.AcceptByID(ctx, carla, lista[0].ID)
		if err != nil || l.Status != domain.InviteAccepted || l.CaregiverID != "carla" {
			t.Fatalf("got %+v err=%v", l, err)
		}
		if resto, _ := w.svc.ListPending(ctx, carla); len(resto) != 1 {
			t.Fatalf("debería quedar 1 pendiente, got %d", len(resto))
		}
	})

	t.Run("dado un usuario sin verificar o con otro email, cuando lista pendientes, entonces no ve las de Carla", func(t *testing.T) {
		w := givenWorld()
		w.invitar(t, "p1", "carla@mail.com")
		if l, _ := w.svc.ListPending(ctx, dario); len(l) != 0 {
			t.Errorf("dario no debería ver nada, got %d", len(l))
		}
		if _, err := w.svc.ListPending(ctx, sinVerific); !errors.Is(err, domain.ErrEmailNotVerified) {
			t.Errorf("sin verificar: esperaba ErrEmailNotVerified, got %v", err)
		}
	})

	t.Run("dada una invitación de Carla, cuando Dario intenta aceptarla o rechazarla por id, entonces no tiene permiso", func(t *testing.T) {
		w := givenWorld()
		inv, _ := w.invitar(t, "p1", "carla@mail.com")
		if _, err := w.svc.AcceptByID(ctx, dario, inv.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("aceptar: got %v", err)
		}
		if err := w.svc.Reject(ctx, dario, inv.ID); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("rechazar: got %v", err)
		}
	})

	t.Run("dada una invitación, cuando Carla la rechaza, entonces ya no figura como pendiente y no da acceso", func(t *testing.T) {
		w := givenWorld()
		inv, tok := w.invitar(t, "p1", "carla@mail.com")
		if err := w.svc.Reject(ctx, carla, inv.ID); err != nil {
			t.Fatal(err)
		}
		if l, _ := w.svc.ListPending(ctx, carla); len(l) != 0 {
			t.Errorf("no debería quedar pendiente, got %d", len(l))
		}
		if _, err := w.svc.Accept(ctx, carla, tok); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("el enlace ya no debería servir, got %v", err)
		}
	})
}

// ---- Revoke / Leave / List / Preview ----

func TestRevokeAndLeave(t *testing.T) {
	ctx := context.Background()

	t.Run("dada una cuidadora aceptada, cuando Ana la quita, entonces deja de ser cuidadora", func(t *testing.T) {
		w := givenWorld()
		l := w.aceptada(t, "p1")
		if err := w.svc.Revoke(ctx, ana, "p1", l.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := w.invites.ByID(ctx, l.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("el vínculo debería haberse eliminado, got %v", err)
		}
	})

	t.Run("dado un vínculo, cuando intenta quitarlo la propia cuidadora o un tercero, entonces no tiene permiso", func(t *testing.T) {
		w := givenWorld()
		l := w.aceptada(t, "p1")
		for _, u := range []*domain.User{carla, dario} {
			if err := w.svc.Revoke(ctx, u, "p1", l.ID); !errors.Is(err, domain.ErrForbidden) {
				t.Errorf("%s: esperaba ErrForbidden, got %v", u.ID, err)
			}
		}
	})

	t.Run("dado un vínculo de otro plan, cuando Ana lo quita indicando p2, entonces no se encuentra", func(t *testing.T) {
		w := givenWorld()
		l := w.aceptada(t, "p1")
		if err := w.svc.Revoke(ctx, ana, "p2", l.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("esperaba ErrNotFound, got %v", err)
		}
	})

	t.Run("dada una cuidadora aceptada, cuando deja de seguir el plan, entonces se elimina su vínculo; si no era cuidadora, no se encuentra", func(t *testing.T) {
		w := givenWorld()
		w.aceptada(t, "p1")
		if err := w.svc.Leave(ctx, carla, "p1"); err != nil {
			t.Fatal(err)
		}
		if err := w.svc.Leave(ctx, carla, "p1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("segunda vez: esperaba ErrNotFound, got %v", err)
		}
		if err := w.svc.Leave(ctx, dario, "p1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("dario: esperaba ErrNotFound, got %v", err)
		}
	})
}

func TestListCaregivers(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un plan con una cuidadora aceptada y una invitación pendiente, cuando lo consultan Ana y la cuidadora, entonces ven ambos vínculos", func(t *testing.T) {
		w := givenWorld()
		w.aceptada(t, "p1")
		w.invitar(t, "p1", "otro@mail.com")
		for _, u := range []*domain.User{ana, carla} {
			l, err := w.svc.ListCaregivers(ctx, u, "p1")
			if err != nil || len(l) != 2 {
				t.Errorf("%s: esperaba 2, got %d err=%v", u.ID, len(l), err)
			}
		}
	})

	t.Run("dado un plan de Ana, cuando lo consulta alguien sin vínculo, entonces no tiene permiso", func(t *testing.T) {
		w := givenWorld()
		w.aceptada(t, "p1")
		if _, err := w.svc.ListCaregivers(ctx, dario, "p1"); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("esperaba ErrForbidden, got %v", err)
		}
	})
}

func TestPreview(t *testing.T) {
	ctx := context.Background()

	t.Run("dado un enlace válido, cuando se abre la página de invitación sin sesión, entonces muestra el nombre del plan y nada más", func(t *testing.T) {
		w := givenWorld()
		_, tok := w.invitar(t, "p1", "carla@mail.com")
		pv, err := w.svc.Preview(ctx, tok)
		if err != nil || pv.PlanName != "Hipertensión" {
			t.Fatalf("got %+v err=%v", pv, err)
		}
	})

	t.Run("dado un enlace vencido, inventado o ya usado, cuando se abre la página, entonces no hay vista previa", func(t *testing.T) {
		w := givenWorld()
		_, tok := w.invitar(t, "p1", "carla@mail.com")
		if _, err := w.svc.Preview(ctx, "inventado"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("inventado: got %v", err)
		}
		*w.clock = t0.Add(8 * 24 * time.Hour)
		if _, err := w.svc.Preview(ctx, tok); !errors.Is(err, domain.ErrInvitationExpired) {
			t.Errorf("vencido: got %v", err)
		}
	})
}
