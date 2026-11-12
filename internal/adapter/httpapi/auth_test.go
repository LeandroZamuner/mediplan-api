package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lea/mediplan-api/internal/domain"
	"github.com/lea/mediplan-api/internal/usecase/auth"
)

type verifier map[string]*auth.Identity

func (v verifier) Verify(_ context.Context, t string) (*auth.Identity, error) {
	if id, ok := v[t]; ok {
		return id, nil
	}
	return nil, errors.New("inválido")
}

type repo map[string]*domain.User

func (r repo) ByFirebaseUID(_ context.Context, uid string) (*domain.User, error) {
	if u, ok := r[uid]; ok {
		return u, nil
	}
	return nil, domain.ErrNotFound
}
func (r repo) SetTimezone(_ context.Context, userID, tz string) error {
	for _, u := range r {
		if u.ID == userID {
			u.Timezone = tz
			return nil
		}
	}
	return domain.ErrNotFound
}
func (r repo) Delete(_ context.Context, userID string) error {
	for uid, u := range r {
		if u.ID == userID {
			delete(r, uid)
			return nil
		}
	}
	return domain.ErrNotFound
}
func (r repo) Create(_ context.Context, u *domain.User) error {
	u.ID = "id-1"
	r[u.FirebaseUID] = u
	return nil
}

func do(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func givenRouter() http.Handler {
	svc := auth.NewService(
		verifier{
			"tok":     {UID: "u1", Email: "ana@mail.com", EmailVerified: true},
			"noverif": {UID: "u2", EmailVerified: false},
		},
		repo{},
	)
	return NewRouter(Handlers{Auth: NewAuthHandler(svc)})
}

func TestHTTPAuth(t *testing.T) {
	cases := []struct {
		name  string // "dado ..., cuando ..., entonces ..."
		setup func(h http.Handler)
		do    func(h http.Handler) *httptest.ResponseRecorder
		want  int
	}{
		{
			name: "dado que no hay token, cuando se pide /me, entonces responde 401",
			do:   func(h http.Handler) *httptest.ResponseRecorder { return do(h, "GET", "/api/v1/me", "", "") },
			want: 401,
		},
		{
			name: "dado un token inválido, cuando se pide /me, entonces responde 401",
			do:   func(h http.Handler) *httptest.ResponseRecorder { return do(h, "GET", "/api/v1/me", "malo", "") },
			want: 401,
		},
		{
			name: "dado un token válido sin rol elegido, cuando se pide /me, entonces responde 403",
			do:   func(h http.Handler) *httptest.ResponseRecorder { return do(h, "GET", "/api/v1/me", "tok", "") },
			want: 403,
		},
		{
			name: "dado un email sin verificar, cuando se registra, entonces responde 403",
			do: func(h http.Handler) *httptest.ResponseRecorder {
				return do(h, "POST", "/api/v1/auth/register", "noverif", `{"role":"patient"}`)
			},
			want: 403,
		},
		{
			name: "dado un rol inválido, cuando se registra, entonces responde 422",
			do: func(h http.Handler) *httptest.ResponseRecorder {
				return do(h, "POST", "/api/v1/auth/register", "tok", `{"role":"admin"}`)
			},
			want: 422,
		},
		{
			name: "dado un JSON roto, cuando se registra, entonces responde 400",
			do: func(h http.Handler) *httptest.ResponseRecorder {
				return do(h, "POST", "/api/v1/auth/register", "tok", `{`)
			},
			want: 400,
		},
		{
			name: "dado un token válido, cuando se registra con rol paciente, entonces responde 200",
			do: func(h http.Handler) *httptest.ResponseRecorder {
				return do(h, "POST", "/api/v1/auth/register", "tok", `{"role":"patient"}`)
			},
			want: 200,
		},
		{
			name:  "dado un usuario registrado, cuando se pide /me, entonces responde 200",
			setup: func(h http.Handler) { do(h, "POST", "/api/v1/auth/register", "tok", `{"role":"patient"}`) },
			do:    func(h http.Handler) *httptest.ResponseRecorder { return do(h, "GET", "/api/v1/me", "tok", "") },
			want:  200,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Given
			h := givenRouter()
			if c.setup != nil {
				c.setup(h)
			}
			// When
			w := c.do(h)
			// Then
			if w.Code != c.want {
				t.Fatalf("esperaba %d, got %d (%s)", c.want, w.Code, w.Body.String())
			}
		})
	}
}

func TestTimezoneHTTP(t *testing.T) {
	t.Run("dado un usuario registrado, cuando informa su zona horaria, entonces /me la devuelve", func(t *testing.T) {
		h := givenAPI()
		if w := do(h, "PATCH", "/api/v1/me", "ana", `{"timezone":"America/Montevideo"}`); w.Code != 200 || !strings.Contains(w.Body.String(), "America/Montevideo") {
			t.Fatalf("patch: %d %s", w.Code, w.Body.String())
		}
		if w := do(h, "GET", "/api/v1/me", "ana", ""); !strings.Contains(w.Body.String(), `"timezone":"America/Montevideo"`) {
			t.Fatalf("me: %s", w.Body.String())
		}
	})

	t.Run("dada una zona inválida o un JSON roto, cuando se informa, entonces 422 y 400", func(t *testing.T) {
		h := givenAPI()
		if w := do(h, "PATCH", "/api/v1/me", "ana", `{"timezone":"Marte/Olympus"}`); w.Code != 422 {
			t.Errorf("inválida: %d", w.Code)
		}
		if w := do(h, "PATCH", "/api/v1/me", "ana", `{`); w.Code != 400 {
			t.Errorf("json roto: %d", w.Code)
		}
	})
}

func TestDeleteAccountHTTP(t *testing.T) {
	t.Run("dado un usuario registrado, cuando elimina su cuenta, entonces 204 y con el mismo token ya no está registrado", func(t *testing.T) {
		h := givenAPI()
		if w := do(h, "DELETE", "/api/v1/me", "ana", ""); w.Code != 204 {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
		if w := do(h, "GET", "/api/v1/me", "ana", ""); w.Code == 200 {
			t.Fatalf("el usuario seguía existiendo: %d", w.Code)
		}
	})

	t.Run("dada una petición sin token, cuando intenta eliminar, entonces 401", func(t *testing.T) {
		h := givenAPI()
		if w := do(h, "DELETE", "/api/v1/me", "", ""); w.Code != 401 {
			t.Fatalf("got %d", w.Code)
		}
	})
}
