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

## Avisos de dosis omitidas (Cloud Scheduler)

Con `JOB_SECRET` definido se habilita `POST /internal/jobs/missed-doses` (header `X-Job-Secret`). Un job de
Cloud Scheduler lo llama cada ~10 minutos: marca como omitidas las tomas vencidas hace más de 30 min y avisa a
los cuidadores del plan (push por FCM + aviso guardado en la app). Sin `JOB_SECRET` el endpoint no existe.
Enviar push requiere credenciales de Firebase (en Cloud Run se toman de la cuenta de servicio del servicio);
sin ellas el envío se simula en el log y los avisos igual se guardan.

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

## Licencia

[MIT](LICENSE): podés usar, copiar y modificar el código manteniendo el aviso de autor, sin garantía.
