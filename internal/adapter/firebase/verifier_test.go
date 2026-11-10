package firebase

import (
	"context"
	"testing"
)

func TestVerifier(t *testing.T) {
	ctx := context.Background()

	t.Run("dado solo el ID del proyecto y ninguna credencial de Google, cuando se crea el verificador, entonces no falla", func(t *testing.T) {
		// Given: sin credenciales en el entorno
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
		t.Setenv("HOME", t.TempDir()) // sin gcloud ni credenciales por defecto
		// When
		v, err := NewVerifier(ctx, "mediplan-test")
		// Then: verificar tokens solo necesita las claves públicas de Google
		if err != nil || v == nil {
			t.Fatalf("no debería pedir credenciales: %v", err)
		}
	})

	t.Run("dado un verificador, cuando recibe un token que no es de Firebase, entonces lo rechaza sin salir a la red", func(t *testing.T) {
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
		t.Setenv("HOME", t.TempDir())
		v, err := NewVerifier(ctx, "mediplan-test")
		if err != nil {
			t.Fatal(err)
		}
		for _, tok := range []string{"", "basura", "a.b.c"} {
			if id, err := v.Verify(ctx, tok); err == nil || id != nil {
				t.Errorf("%q: debería rechazarse, got %+v", tok, id)
			}
		}
	})
}
