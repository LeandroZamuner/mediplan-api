package config

import (
	"os"
	"strconv"
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

	// Correos de cuenta propios (recuperar contraseña, verificar correo). Si falta algo, la API no publica esas rutas
	// y las apps usan el correo estándar de Firebase.
	// WebBaseURL: raíz de la web a la que apuntan los enlaces (ej. https://usuario.github.io/mediplan-web).
	WebBaseURL string
	// FirebaseCredentials: JSON de la clave de cuenta de servicio (SECRETO: solo en variables del hosting, nunca en el repo).
	FirebaseCredentials string
	// BrevoAPIKey (SECRETO), MailFrom (remitente verificado en Brevo) y MailFromName.
	BrevoAPIKey  string
	MailFrom     string
	MailFromName string

	// Pagos con Mercado Pago. Sin MPAccessToken (o sin WebBaseURL) los pagos quedan deshabilitados.
	// MPAccessToken: Access Token de PRODUCCIÓN (SECRETO). MPWebhookSecret: clave que firma los avisos (SECRETO).
	MPAccessToken   string
	MPWebhookSecret string
	// PriceARS: precio mensual de Premium en pesos.
	PriceARS int
	// APIPublicURL: dirección pública de esta API (para que Mercado Pago avise cada cobro). En Render se toma sola.
	APIPublicURL string
}

func Load() Config {
	return Config{
		Port:                get("PORT", "8080"),
		DatabaseURL:         get("DATABASE_URL", "postgres://mediplan:mediplan@localhost:5432/mediplan?sslmode=disable"),
		Env:                 get("APP_ENV", "development"),
		FirebaseProjectID:   get("FIREBASE_PROJECT_ID", "mediplan-dev"),
		JobSecret:           os.Getenv("JOB_SECRET"),
		WebOrigins:          splitList(os.Getenv("WEB_ORIGINS")),
		WebBaseURL:          os.Getenv("WEB_BASE_URL"),
		FirebaseCredentials: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
		BrevoAPIKey:         os.Getenv("BREVO_API_KEY"),
		MailFrom:            os.Getenv("MAIL_FROM"),
		MailFromName:        get("MAIL_FROM_NAME", "MediPlan"),
		MPAccessToken:       os.Getenv("MP_ACCESS_TOKEN"),
		MPWebhookSecret:     os.Getenv("MP_WEBHOOK_SECRET"),
		PriceARS:            atoi(get("PREMIUM_PRICE_ARS", "2999"), 2999),
		APIPublicURL:        strings.TrimRight(get("API_PUBLIC_URL", os.Getenv("RENDER_EXTERNAL_URL")), "/"),
	}
}

func get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func atoi(v string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
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
