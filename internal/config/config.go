package config

import (
	"os"
	"strings"
)

// Config se lee solo desde variables de entorno para que el contenedor
// sea portable entre hostings (Cloud Run, AWS, Railway, local).
type Config struct {
	Port        string
	DatabaseURL string
	Env         string
	// FirebaseProjectID es el ID del proyecto de Firebase para validar tokens.
	FirebaseProjectID string
	// JobSecret protege las tareas programadas (Cloud Scheduler). Vacío = tareas deshabilitadas.
	JobSecret string
	// WebOrigins: sitios web autorizados a llamar a la API desde el navegador (CORS), separados por coma.
	// Por ejemplo: https://usuario.github.io,http://localhost:3000. Vacío = CORS apagado.
	WebOrigins []string
}

func Load() Config {
	return Config{
		Port:              get("PORT", "8080"),
		DatabaseURL:       get("DATABASE_URL", "postgres://mediplan:mediplan@localhost:5432/mediplan?sslmode=disable"),
		Env:               get("APP_ENV", "development"),
		FirebaseProjectID: get("FIREBASE_PROJECT_ID", "mediplan-dev"),
		JobSecret:         os.Getenv("JOB_SECRET"),
		WebOrigins:        splitList(os.Getenv("WEB_ORIGINS")),
	}
}

func get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
