package domain

import "errors"

// Preferences son las elecciones de la persona sobre las funciones con IA y los correos. Todo arranca apagado:
// hay que activarlo a propósito (consentimiento).
type Preferences struct {
	// WeeklyReport: recibir por correo el resumen semanal (solo Premium).
	WeeklyReport bool
	// AIConsent: acepta que un modelo de IA de un tercero (Google Gemini) lea datos de su tratamiento para redactar
	// el resumen y responder en el asistente.
	AIConsent bool
}

var (
	ErrPremiumRequired = errors.New("esta función es parte de Premium")
	ErrConsentRequired = errors.New("para usar la inteligencia artificial primero tenés que aceptarlo en tus preferencias")
	ErrInvalidMessage  = errors.New("mensaje inválido")
	ErrAIUnavailable   = errors.New("el asistente no está disponible en este momento, probá de nuevo más tarde")
)
