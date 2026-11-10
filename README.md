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

## Licencia

[MIT](LICENSE): podés usar, copiar y modificar el código manteniendo el aviso de autor, sin garantía.
