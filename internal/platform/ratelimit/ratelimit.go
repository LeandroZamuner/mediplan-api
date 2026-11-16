// Package ratelimit limita cuántas veces se puede hacer algo por clave dentro de una ventana de tiempo.
// Vive en memoria: alcanza para una sola instancia (el plan gratis de Render); con varias instancias
// cada una llevaría su propia cuenta.
package ratelimit

import (
	"sync"
	"time"
)

// sweepAt es la cantidad de claves a partir de la cual se descartan las vencidas, para que el mapa no crezca sin límite.
const sweepAt = 5000

type Limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
	now  func() time.Time
	// longest es la ventana más larga usada: ninguna clave se descarta antes de que venza.
	longest time.Duration
}

func New() *Limiter { return &Limiter{hits: map[string][]time.Time{}, now: time.Now} }

// Allow cuenta un intento de key y dice si entra dentro de "max veces cada window".
// Los intentos rechazados no se cuentan.
func (l *Limiter) Allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if window > l.longest {
		l.longest = window
	}
	if len(l.hits) >= sweepAt {
		l.sweep(now, l.longest)
	}
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

func (l *Limiter) sweep(now time.Time, window time.Duration) {
	for k, ts := range l.hits {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= window {
			delete(l.hits, k)
		}
	}
}

// Undo descarta el último intento aceptado de key (por ejemplo, si el envío falló y no debe castigarse al usuario).
func (l *Limiter) Undo(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts := l.hits[key]; len(ts) > 0 {
		l.hits[key] = ts[:len(ts)-1]
	}
}
