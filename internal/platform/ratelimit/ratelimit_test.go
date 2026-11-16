package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	t.Run("dado un límite de 2 por minuto, cuando se pide una tercera vez, entonces se rechaza", func(t *testing.T) {
		// Given
		l := New()
		// When
		a, b, c := l.Allow("k", 2, time.Minute), l.Allow("k", 2, time.Minute), l.Allow("k", 2, time.Minute)
		// Then
		if !a || !b || c {
			t.Fatalf("esperaba true,true,false y fue %v,%v,%v", a, b, c)
		}
	})

	t.Run("dadas claves distintas, cuando una agota su cupo, entonces la otra no se ve afectada", func(t *testing.T) {
		l := New()
		l.Allow("a", 1, time.Minute)
		if !l.Allow("b", 1, time.Minute) {
			t.Fatal("la clave b no debería estar limitada")
		}
	})

	t.Run("dado un cupo agotado, cuando pasa la ventana, entonces vuelve a permitir", func(t *testing.T) {
		l := New()
		now := time.Now()
		l.now = func() time.Time { return now }
		l.Allow("k", 1, time.Minute)
		if l.Allow("k", 1, time.Minute) {
			t.Fatal("debía rechazar dentro de la ventana")
		}
		l.now = func() time.Time { return now.Add(61 * time.Second) }
		if !l.Allow("k", 1, time.Minute) {
			t.Fatal("debía permitir pasada la ventana")
		}
	})

	t.Run("dadas muchas claves vencidas, cuando se sigue usando, entonces el mapa se limpia", func(t *testing.T) {
		l := New()
		now := time.Now()
		l.now = func() time.Time { return now }
		for i := 0; i < sweepAt+10; i++ {
			l.Allow(time.Duration(i).String(), 1, time.Minute)
		}
		l.now = func() time.Time { return now.Add(time.Hour) }
		l.Allow("nueva", 1, time.Minute)
		if len(l.hits) > 2 {
			t.Fatalf("quedaron %d claves vencidas", len(l.hits))
		}
	})

	t.Run("dado un intento aceptado, cuando se deshace, entonces el cupo vuelve a estar disponible", func(t *testing.T) {
		l := New()
		l.Allow("k", 1, time.Minute)
		l.Undo("k")
		if !l.Allow("k", 1, time.Minute) {
			t.Fatal("debía permitir tras deshacer")
		}
	})
}
