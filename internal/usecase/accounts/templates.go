package accounts

import (
	"bytes"
	"html/template"
)

type content struct{ ActionURL, LogoURL string }

type texts struct {
	Subject, Title, Intro, Button, Note string
}

var resetCopy = texts{
	Subject: "Restablecé tu contraseña de MediPlan",
	Title:   "Restablecé tu contraseña",
	Intro:   "Recibimos un pedido para cambiar la contraseña de tu cuenta de MediPlan. Tocá el botón para elegir una nueva.",
	Button:  "Restablecer contraseña",
	Note:    "Por seguridad, el enlace vence en poco tiempo y solo se puede usar una vez. Si vos no lo pediste, ignorá este correo: tu contraseña no cambia.",
}

var verifyCopy = texts{
	Subject: "Confirmá tu correo en MediPlan",
	Title:   "Confirmá tu correo",
	Intro:   "¡Te damos la bienvenida a MediPlan! Para terminar de crear tu cuenta, confirmá que este correo es tuyo.",
	Button:  "Confirmar mi correo",
	Note:    "Si el enlace venció, pedí uno nuevo desde la app. Si no creaste una cuenta en MediPlan, ignorá este correo.",
}

// Colores de la marca (los mismos que la web): azul #3646ab, azul oscuro #27327c, gris de fondo #eff0f1.
var layout = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title></head>
<body style="margin:0;padding:0;background:#eff0f1;font-family:Arial,Helvetica,sans-serif;color:#111827;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#eff0f1;padding:24px 12px;"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#ffffff;border-radius:16px;overflow:hidden;">
<tr><td align="center" style="padding:28px 24px 8px;"><img src="{{.LogoURL}}" alt="MediPlan" width="180" style="display:block;border:0;font-size:24px;font-weight:bold;color:#3646ab;"></td></tr>
<tr><td style="padding:16px 32px 0;"><h1 style="margin:0 0 12px;font-size:22px;line-height:1.3;color:#27327c;">{{.Title}}</h1>
<p style="margin:0 0 24px;font-size:16px;line-height:1.5;color:#3a404d;">{{.Intro}}</p></td></tr>
<tr><td align="center" style="padding:0 32px 24px;"><table role="presentation" cellpadding="0" cellspacing="0"><tr><td align="center" bgcolor="#3646ab" style="border-radius:12px;"><a href="{{.ActionURL}}" style="display:inline-block;padding:14px 28px;font-size:16px;font-weight:bold;color:#ffffff;text-decoration:none;border-radius:12px;">{{.Button}}</a></td></tr></table></td></tr>
<tr><td style="padding:0 32px 8px;"><p style="margin:0;font-size:13px;line-height:1.5;color:#3a404d;">¿El botón no funciona? Copiá y pegá este enlace en tu navegador:</p>
<p style="margin:4px 0 0;font-size:12px;line-height:1.5;word-break:break-all;"><a href="{{.ActionURL}}" style="color:#3646ab;">{{.ActionURL}}</a></p></td></tr>
<tr><td style="padding:16px 32px 28px;"><p style="margin:0;font-size:13px;line-height:1.5;color:#6b7280;">{{.Note}}</p></td></tr>
</table>
<p style="margin:16px 0 0;font-size:12px;color:#6b7280;">MediPlan · Tu plan de medicación siempre bajo control</p>
</td></tr></table></body></html>`))

func render(c texts, ct content) Message {
	var b bytes.Buffer
	_ = layout.Execute(&b, struct {
		texts
		content
	}{c, ct})
	text := c.Title + "\n\n" + c.Intro + "\n\n" + c.Button + ": " + ct.ActionURL + "\n\n" + c.Note + "\n\nMediPlan"
	return Message{Subject: c.Subject, HTML: b.String(), Text: text}
}

func resetMessage(ct content) Message  { return render(resetCopy, ct) }
func verifyMessage(ct content) Message { return render(verifyCopy, ct) }
