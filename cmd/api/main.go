package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // zonas horarias embebidas: el contenedor no trae la base del sistema

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lea/mediplan-api/internal/adapter/brevo"
	"github.com/lea/mediplan-api/internal/adapter/firebase"
	"github.com/lea/mediplan-api/internal/app"
	"github.com/lea/mediplan-api/internal/config"
	"github.com/lea/mediplan-api/internal/platform/mail"
	"github.com/lea/mediplan-api/internal/platform/push"
	"github.com/lea/mediplan-api/internal/usecase/accounts"
	"github.com/lea/mediplan-api/internal/usecase/notifications"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("base de datos: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("no se pudo conectar a la base de datos: %v", err)
	}

	verifier, err := firebase.NewVerifier(ctx, cfg.FirebaseProjectID)
	if err != nil {
		log.Fatalf("firebase: %v", err)
	}

	// Enviar push requiere credenciales (Cloud Run las trae solas). Sin ellas, los avisos
	// se guardan igual y el envío queda simulado en el log: útil en desarrollo.
	var pusher notifications.Pusher = push.LogPusher{}
	if p, err := firebase.NewPusher(ctx, cfg.FirebaseProjectID); err != nil {
		log.Printf("push simulado (sin credenciales de Firebase para enviar): %v", err)
	} else {
		pusher = p
	}
	if cfg.JobSecret == "" {
		log.Printf("JOB_SECRET vacío: las tareas programadas (dosis omitidas) están deshabilitadas")
	}

	// Correos de cuenta con diseño propio: hace falta clave de Brevo, remitente, URL de la web y credenciales de Firebase
	// (o el emulador). Si falta algo, esas rutas no se publican y las apps usan el correo estándar de Firebase.
	var accountsSvc *accounts.Service
	_, emulator := os.LookupEnv("FIREBASE_AUTH_EMULATOR_HOST")
	var mailer accounts.Mailer
	switch {
	case cfg.BrevoAPIKey != "" && cfg.MailFrom != "":
		mailer = brevo.New(cfg.BrevoAPIKey, cfg.MailFrom, cfg.MailFromName)
	case emulator:
		mailer = mail.LogMailer{} // desarrollo: el correo queda en el log
	}
	if mailer != nil && cfg.WebBaseURL != "" && (cfg.FirebaseCredentials != "" || emulator) {
		links, err := firebase.NewLinks(ctx, cfg.FirebaseProjectID, []byte(cfg.FirebaseCredentials), emulator)
		if err != nil {
			log.Printf("correos propios deshabilitados: %v", err)
		} else {
			accountsSvc = accounts.NewService(verifier, links, mailer, accounts.Config{WebBaseURL: cfg.WebBaseURL})
		}
	} else {
		log.Printf("correos propios deshabilitados (faltan BREVO_API_KEY, MAIL_FROM, WEB_BASE_URL o FIREBASE_CREDENTIALS_JSON): se usa el correo estándar de Firebase")
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.NewHandler(app.Deps{Pool: pool, Verifier: verifier, Pusher: pusher, JobSecret: cfg.JobSecret, AllowedOrigins: cfg.WebOrigins, Accounts: accountsSvc}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Printf("mediplan-api escuchando en :%s (env=%s)", cfg.Port, cfg.Env)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
