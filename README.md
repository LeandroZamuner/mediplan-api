# mediplan-api

API de MediPlan en Go (clean architecture liviana). Auth con Firebase, datos en PostgreSQL.

## Correr en local

```bash
# Docker Desktop instalado. Si `docker` no está en el PATH:
export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"

docker compose up -d db                       # Postgres 16 (usuario/clave/base: mediplan)

# Aplicar la migración (hasta tener goose):
sed '/-- +goose Down/,$d' migrations/001_init.sql | docker compose exec -T db psql -U mediplan -d mediplan -v ON_ERROR_STOP=1

FIREBASE_PROJECT_ID=<id-del-proyecto> go run ./cmd/api   # escucha en :8080
```

Variables de entorno: `PORT` (8080), `DATABASE_URL`, `APP_ENV`, `FIREBASE_PROJECT_ID`, `JOB_SECRET`, `WEB_ORIGINS`.

`WEB_ORIGINS` lista (separados por coma) los sitios web que pueden llamar a la API desde el navegador (CORS), por ejemplo
`https://usuario.github.io,http://localhost:3000`. Se comparan de forma exacta y sin ella CORS queda apagado. La app móvil y
Cloud Scheduler no usan navegador, así que no dependen de esta variable.
Para desarrollar sin Firebase real se puede usar el emulador de Auth (`FIREBASE_AUTH_EMULATOR_HOST`).

## Avisos de dosis omitidas

Con `JOB_SECRET` definido se habilita `POST /internal/jobs/missed-doses` (header `X-Job-Secret`). Una tarea
programada (GitHub Actions o Cloud Scheduler) lo llama cada 5 minutos: marca como omitidas las tomas vencidas hace
más de 30 min y avisa **a la paciente** ("No registraste …") **y a cada cuidador** del plan (push por FCM + aviso
guardado en la app). Los planes sin cuidadores también se procesan. Sin `JOB_SECRET` el endpoint no existe.
Enviar push requiere credenciales de Firebase (en Cloud Run se toman de la cuenta de servicio del servicio);
sin ellas el envío se simula en el log y los avisos igual se guardan.

## Correos de cuenta (verificar correo y recuperar contraseña)

Firebase solo genera el enlace seguro (un código de un solo uso); el correo con el diseño de MediPlan lo arma y envía esta API,
y el botón lleva a la página `/accion` de la web (no a la pantalla genérica de Firebase).

- `POST /api/v1/auth/password-reset` `{"email"}`: público. Responde siempre `202` exista o no la cuenta (no revela quién tiene cuenta).
  Límites: 1 correo por dirección por minuto y 10 pedidos por IP cada 15 minutos (`429`).
- `POST /api/v1/auth/email-verification`: con el ID token (aunque el correo aún no esté verificado). `{"sent":true}`, o
  `{"sent":false,"alreadyVerified":true}` si ya estaba verificado. 1 por minuto por usuario (`429`).
- Si el envío falla responde `503` y las apps usan el correo estándar de Firebase como respaldo. Si falta configuración, las rutas
  no se publican (`404`) y pasa lo mismo.

Variables (todas necesarias; **las dos marcadas son secretos: solo en el panel del hosting, nunca en el repo ni en el chat**):

| Variable | Qué es |
| --- | --- |
| `WEB_BASE_URL` | Raíz de la web, p. ej. `https://leandrozamuner.github.io/mediplan-web` |
| `BREVO_API_KEY` 🔒 | Clave de la API de Brevo (plan gratis: 300 correos por día) |
| `MAIL_FROM` / `MAIL_FROM_NAME` | Remitente verificado en Brevo / nombre que se muestra (`MediPlan`) |
| `FIREBASE_CREDENTIALS_JSON` 🔒 | Contenido del JSON de la cuenta de servicio de Firebase (para generar los enlaces) |

Se usa la API HTTPS de Brevo y no SMTP porque **Render bloquea el SMTP saliente (puertos 25, 465 y 587) en el plan gratuito**.
En local, con el emulador de Auth (`FIREBASE_AUTH_EMULATOR_HOST`) y `WEB_BASE_URL`, no hacen falta claves: el correo se escribe en el log.

## Plan gratis y Premium

Cada cuenta es gratis o Premium (tabla `subscriptions`; sin fila = gratis). Los topes **solo frenan crear** cosas nuevas: nunca se borra
ni se bloquea la edición de lo que ya existe.

| | Gratis | Premium |
| --- | --- | --- |
| Planes activos | 1 | sin tope |
| Medicamentos (entre sus planes activos) | 3 | sin tope |
| Cuidadores por plan (pendientes o aceptados) | 1 | sin tope |

- Rige siempre el nivel del **dueño del plan** (aunque agregue una cuidadora).
- Superar un tope responde `402` con `{"error", "code":"plan_limit", "resource":"plans|medications|caregivers", "limit"}`.
- `GET /api/v1/me/subscription` devuelve `tier`, `cancelled`, `currentPeriodEnd`, `limits` (`null` = sin tope) y `usage`.
- Una suscripción activa con el período vencido hace menos de 3 días sigue siendo Premium (cobro demorado); una cancelada
  lo es hasta que termina el período pagado.

## Pago de Premium con Mercado Pago

Premium se cobra con una **suscripción mensual de Mercado Pago** (pesos argentinos). La cuenta **sigue siendo gratis hasta que el pago se confirma**.

| Ruta | Qué hace |
|---|---|
| `POST /api/v1/me/subscription/checkout` | Crea la suscripción pendiente y devuelve `{checkoutUrl}` para pagar. Acepta `{"payerEmail"}` (correo de la cuenta de Mercado Pago de quien paga). `409` si ya es Premium. |
| `POST /api/v1/me/subscription/sync` | La app lo llama al volver de pagar: consulta a Mercado Pago y devuelve el estado. |
| `DELETE /api/v1/me/subscription` | Baja: deja de renovarse y sigue Premium hasta el fin del período pagado. |
| `POST /api/v1/webhooks/mercadopago` | Aviso de Mercado Pago (público). Se valida la firma `x-signature` y **además** se consulta el estado real a Mercado Pago: un aviso falso no puede dar Premium. |
| `GET /api/v1/me/subscription` | Suma `pending` (pago iniciado sin confirmar) y `payments: {enabled, priceArs, currency, period}`. Si hay algo pendiente o el período está por vencer, se actualiza solo desde Mercado Pago. |

Variables de entorno (en el hosting, nunca en el repo):

| Variable | Para qué |
|---|---|
| `MP_ACCESS_TOKEN` | Access Token de **producción** de la aplicación de Mercado Pago (secreto). Sin él, los pagos quedan deshabilitados. |
| `MP_WEBHOOK_SECRET` | Clave secreta de los webhooks (secreto). |
| `PREMIUM_PRICE_ARS` | Precio mensual en pesos (por defecto 2999). |
| `API_PUBLIC_URL` | URL pública de esta API para los avisos (en Render se toma sola de `RENDER_EXTERNAL_URL`). |
| `WEB_BASE_URL` | Ya existente: a esa web vuelve la persona después de pagar (`/premium/`). |

## Migraciones

Los archivos de `migrations/` se aplican **en orden** (`001`, `002`, …) y son idempotentes desde la 002. En una base ya creada alcanza con aplicar
las nuevas **antes** de desplegar el código que las usa:

```bash
read -rs DATABASE_URL            # pegá la URL de conexión y Enter
sed '/-- +goose Down/,$d' migrations/002_subscriptions.sql | docker run --rm -i postgres:16 psql "$DATABASE_URL" -v ON_ERROR_STOP=1
```

## Tests

```bash
go test ./...
```

Los tests de integración (`internal/adapter/postgres`, `internal/app`) crean una base temporal
`mediplan_test_<pid>` en el Postgres de Docker y la borran al terminar; no tocan la base de desarrollo.
Si no hay Postgres levantado, esos tests se saltean. Los tests se escriben con TDD y estilo
Given/When/Then.

## Estructura

```
cmd/api                  punto de entrada
internal/domain          entidades y errores del negocio
internal/usecase/*       casos de uso y puertos (auth, plans, caregivers)
internal/adapter/*       httpapi, firebase, postgres
internal/platform/*      utilidades (tokens de invitación)
internal/app             ensamblado de todas las piezas
migrations               esquema SQL
```

## Despliegue (Cloud Run + Neon + Cloud Scheduler)

La API se empaqueta con el `Dockerfile` (imagen de ~57 MB, Go estático). Servicios usados, todos con plan gratuito:

- **Neon** (PostgreSQL): plan Free, sin tarjeta. La base se "duerme" tras unos minutos sin uso y despierta sola.
- **Google Cloud Run** (la API): requiere una cuenta de facturación de Google Cloud (tarjeta) aunque el uso quede dentro de la cuota gratuita.
  Poner un **presupuesto con alerta** (por ejemplo USD 1) en la consola de facturación.
- **Cloud Scheduler**: llama cada 5 minutos a `POST /internal/jobs/missed-doses` (detecta tomas omitidas y avisa a los cuidadores).

Variables de entorno: `DATABASE_URL` (secreto), `JOB_SECRET` (secreto), `FIREBASE_PROJECT_ID`, `WEB_ORIGINS`, `APP_ENV=production`; `PORT` lo pone Cloud Run.
**Nunca** poner `DATABASE_URL` ni `JOB_SECRET` en el repo ni en el chat: van en Secret Manager.

```bash
# 1) Base: crear el proyecto en neon.com y aplicar el esquema (la URL queda en una variable de tu terminal, no en un archivo)
read -rs DATABASE_URL            # pegá la URL de conexión y Enter
sed '/-- +goose Down/,$d' migrations/001_init.sql | docker run --rm -i postgres:16 psql "$DATABASE_URL" -v ON_ERROR_STOP=1

# 2) Google Cloud (mismo proyecto de Firebase: así la API tiene credenciales para enviar push)
gcloud auth login
gcloud config set project mediplan-4f001
gcloud services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com cloudscheduler.googleapis.com

# 3) Secretos
printf '%s' "$DATABASE_URL" | gcloud secrets create DATABASE_URL --data-file=-
JOB_SECRET=$(openssl rand -hex 24); printf '%s' "$JOB_SECRET" | gcloud secrets create JOB_SECRET --data-file=-
PROJECT_NUMBER=$(gcloud projects describe mediplan-4f001 --format='value(projectNumber)')
for s in DATABASE_URL JOB_SECRET; do
  gcloud secrets add-iam-policy-binding $s --member="serviceAccount:${PROJECT_NUMBER}-compute@developer.gserviceaccount.com" --role=roles/secretmanager.secretAccessor
done

# 4) Desplegar (construye la imagen en la nube desde este repo)
gcloud run deploy mediplan-api --source . --region southamerica-east1 --allow-unauthenticated \
  --min-instances 0 --max-instances 2 --memory 256Mi \
  --set-env-vars FIREBASE_PROJECT_ID=mediplan-4f001,APP_ENV=production,WEB_ORIGINS=https://leandrozamuner.github.io \
  --set-secrets DATABASE_URL=DATABASE_URL:latest,JOB_SECRET=JOB_SECRET:latest

# 5) Tarea programada (usa la URL que imprime el paso anterior)
API_URL=$(gcloud run services describe mediplan-api --region southamerica-east1 --format='value(status.url)')
gcloud scheduler jobs create http missed-doses --location southamerica-east1 --schedule="*/5 * * * *" \
  --uri="$API_URL/internal/jobs/missed-doses" --http-method=POST --headers="X-Job-Secret=$JOB_SECRET"

# 6) Comprobar
curl "$API_URL/healthz"            # {"status":"ok"}
```

Después: en el repo `mediplan-web` cargar las variables de Actions `NEXT_PUBLIC_API_URL=$API_URL` (y las de Firebase) y volver a correr "Pages";
en Firebase → Authentication → Configuración → Dominios autorizados agregar `leandrozamuner.github.io`.

### Alternativa sin tarjeta: Render + GitHub Actions

Es lo que usa la demo de la tesis. **Render** (Web Service, runtime Docker, plan Free) construye el `Dockerfile` desde `main`; las variables
de entorno son las mismas y se cargan en el panel de Render. Health check: `/healthz`.

- Limitación: el plan Free se duerme tras ~15 min sin uso y la primera consulta tarda alrededor de un minuto en despertar.
- El job de tomas perdidas lo dispara [`.github/workflows/missed-doses.yml`](.github/workflows/missed-doses.yml) cada 5 minutos
  (GitHub puede demorar algunas ejecuciones). Requiere en el repo: variable de Actions `API_URL` (URL pública de Render, sin barra final)
  y secreto `JOB_SECRET` (el mismo valor que en Render).

## Licencia

[MIT](LICENSE): podés usar, copiar y modificar el código manteniendo el aviso de autor, sin garantía.
