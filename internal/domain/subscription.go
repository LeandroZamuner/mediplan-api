package domain

import (
	"errors"
	"fmt"
	"time"
)

// Tier es el nivel de la cuenta: gratis o Premium (de pago, por suscripción).
type Tier string

const (
	TierFree    Tier = "free"
	TierPremium Tier = "premium"
)

type SubStatus string

const (
	SubActive    SubStatus = "active"
	SubCancelled SubStatus = "cancelled" // no se renueva; sigue vigente hasta el fin del período ya pagado
)

// Subscription es la suscripción de un usuario (el que paga es el paciente dueño de los planes).
// Provider/ProviderRef identifican la suscripción en el medio de pago (Mercado Pago).
type Subscription struct {
	UserID           string
	Tier             Tier
	Status           SubStatus
	Provider         string
	ProviderRef      string
	CurrentPeriodEnd *time.Time
	UpdatedAt        time.Time
}

// Unlimited en un límite significa "sin tope".
const Unlimited = -1

// Limits son los topes de cada nivel. Solo frenan CREAR cosas nuevas: nunca se borra ni se oculta lo que ya existe.
type Limits struct {
	MaxActivePlans       int
	MaxMedications       int // entre todos sus planes activos
	MaxCaregiversPerPlan int // vínculos pendientes o aceptados
}

func LimitsFor(t Tier) Limits {
	if t == TierPremium {
		return Limits{Unlimited, Unlimited, Unlimited}
	}
	return Limits{MaxActivePlans: 1, MaxMedications: 3, MaxCaregiversPerPlan: 1}
}

// Recursos limitados (para el cliente).
const (
	ResourcePlans       = "plans"
	ResourceMedications = "medications"
	ResourceCaregivers  = "caregivers"
)

// ErrPlanLimit se devuelve (envuelto en PlanLimitError) al superar un tope del plan gratuito.
var ErrPlanLimit = errors.New("llegaste al límite del plan gratuito")

type PlanLimitError struct {
	Resource string
	Limit    int
}

func (e *PlanLimitError) Error() string {
	what := map[string]string{
		ResourcePlans:       "solo podés tener %d plan activo",
		ResourceMedications: "podés cargar hasta %d medicamentos",
		ResourceCaregivers:  "podés invitar hasta %d cuidador por plan",
	}[e.Resource]
	return fmt.Sprintf("Llegaste al límite del plan gratuito: "+what+". Pasate a Premium para quitar el límite.", e.Limit)
}

func (e *PlanLimitError) Is(target error) bool { return target == ErrPlanLimit }

// Exceeds dice si agregar "adding" a lo que ya hay "used" supera el tope max.
func Exceeds(used, adding, max int) bool { return max != Unlimited && used+adding > max }
