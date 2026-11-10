package domain

import (
	"testing"
	"time"
)

// Calendario de referencia (2026): domingo 8/nov, lunes 9, miércoles 11, jueves 12, domingo 15.
// Argentina es UTC-3 todo el año, así que 08:00 local = 11:00 UTC.

func loc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func utc(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }

func enalapril() Medication {
	return Medication{
		Name: "Enalapril", StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		Schedules: []Schedule{{DayOfWeek: 1, TimeOfDay: "08:00"}, {DayOfWeek: 4, TimeOfDay: "20:30"}}, // lunes y jueves
	}
}

func equalTimes(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func TestOccurrences(t *testing.T) {
	ba := func() *time.Location { return loc(t, "America/Argentina/Buenos_Aires") }

	t.Run("dado un medicamento de lunes 08:00 y jueves 20:30, cuando se pide la semana del 9 al 16, entonces son 2 tomas en hora local de Argentina", func(t *testing.T) {
		got := Occurrences(enalapril(), ba(), utc(9, 0, 0), utc(16, 0, 0), time.Time{})
		want := []time.Time{utc(9, 11, 0), utc(12, 23, 30)} // 08:00 y 20:30 hora argentina, en UTC
		if !equalTimes(got, want) {
			t.Fatalf("esperaba %v, got %v", want, got)
		}
	})

	t.Run("dado el mismo horario, cuando el paciente está en otra zona horaria, entonces los instantes cambian", func(t *testing.T) {
		ny := loc(t, "America/New_York") // 8/nov domingo: ya sin horario de verano (UTC-5)
		got := Occurrences(enalapril(), ny, utc(9, 0, 0), utc(16, 0, 0), time.Time{})
		want := []time.Time{utc(9, 13, 0), utc(13, 1, 30)} // lunes 08:00 NY = 13:00 UTC; jueves 20:30 NY = viernes 01:30 UTC
		if !equalTimes(got, want) {
			t.Fatalf("esperaba %v, got %v", want, got)
		}
	})

	t.Run("dado un medicamento que empieza el miércoles 11, cuando se pide la semana, entonces el lunes anterior no cuenta", func(t *testing.T) {
		m := enalapril()
		m.StartDate = time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
		got := Occurrences(m, ba(), utc(9, 0, 0), utc(16, 0, 0), time.Time{})
		if !equalTimes(got, []time.Time{utc(12, 23, 30)}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("dado un medicamento que termina el jueves 12, cuando se pide la semana, entonces ese jueves cuenta (fin inclusivo); si termina el miércoles, no", func(t *testing.T) {
		m := enalapril()
		finJueves := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
		m.EndDate = &finJueves
		if got := Occurrences(m, ba(), utc(9, 0, 0), utc(16, 0, 0), time.Time{}); len(got) != 2 {
			t.Errorf("fin jueves: esperaba 2, got %v", got)
		}
		finMiercoles := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
		m.EndDate = &finMiercoles
		if got := Occurrences(m, ba(), utc(9, 0, 0), utc(16, 0, 0), time.Time{}); !equalTimes(got, []time.Time{utc(9, 11, 0)}) {
			t.Errorf("fin miércoles: got %v", got)
		}
	})

	t.Run("dado un plan creado el jueves 12 a las 21:00 UTC, cuando se usa como límite, entonces las tomas anteriores no cuentan", func(t *testing.T) {
		got := Occurrences(enalapril(), ba(), utc(9, 0, 0), utc(16, 0, 0), utc(12, 21, 0))
		if !equalTimes(got, []time.Time{utc(12, 23, 30)}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("dada una ventana, cuando una toma cae justo en el inicio o justo en el fin, entonces el inicio entra y el fin no (intervalo [desde, hasta))", func(t *testing.T) {
		m := enalapril()
		if got := Occurrences(m, ba(), utc(9, 11, 0), utc(12, 23, 30), time.Time{}); !equalTimes(got, []time.Time{utc(9, 11, 0)}) {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("dados varios horarios en un mismo día y el domingo como día 0, cuando se piden, entonces salen ordenados", func(t *testing.T) {
		m := Medication{
			StartDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
			Schedules: []Schedule{{DayOfWeek: 0, TimeOfDay: "21:00"}, {DayOfWeek: 0, TimeOfDay: "09:00"}},
		}
		got := Occurrences(m, ba(), utc(15, 0, 0), utc(16, 1, 0), time.Time{}) // domingo 15 (21:00 local = 00:00 UTC del 16)
		want := []time.Time{utc(15, 12, 0), utc(16, 0, 0)}                     // 09:00 y 21:00 locales
		if !equalTimes(got, want) {
			t.Fatalf("esperaba %v, got %v", want, got)
		}
	})

	t.Run("dado un medicamento sin horarios o una ventana vacía, cuando se piden, entonces no hay tomas", func(t *testing.T) {
		if got := Occurrences(Medication{StartDate: utc(1, 0, 0)}, ba(), utc(9, 0, 0), utc(16, 0, 0), time.Time{}); len(got) != 0 {
			t.Errorf("sin horarios: %v", got)
		}
		if got := Occurrences(enalapril(), ba(), utc(16, 0, 0), utc(9, 0, 0), time.Time{}); len(got) != 0 {
			t.Errorf("ventana invertida: %v", got)
		}
	})
}
