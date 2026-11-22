package reports

import (
	"bytes"
	"fmt"
	"html/template"
	"time"

	"github.com/lea/mediplan-api/internal/usecase/accounts"
)

type view struct {
	Title, Summary, Range, AppURL, LogoURL string
	Meds                                   []medView
	Low                                    []string
}

type medView struct{ Name, Result string }

var layout = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title></head>
<body style="margin:0;padding:0;background:#eff0f1;font-family:Arial,Helvetica,sans-serif;color:#111827;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#eff0f1;padding:24px 12px;"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;background:#ffffff;border-radius:16px;overflow:hidden;">
<tr><td align="center" style="padding:28px 24px 8px;"><img src="{{.LogoURL}}" alt="MediPlan" width="180" style="display:block;border:0;font-size:24px;font-weight:bold;color:#3646ab;"></td></tr>
<tr><td style="padding:16px 32px 0;"><h1 style="margin:0 0 4px;font-size:22px;line-height:1.3;color:#27327c;">{{.Title}}</h1>
<p style="margin:0 0 16px;font-size:13px;color:#6b7280;">{{.Range}}</p>
<p style="margin:0 0 20px;font-size:16px;line-height:1.5;color:#3a404d;">{{.Summary}}</p></td></tr>
<tr><td style="padding:0 32px 8px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;color:#3a404d;">
{{range .Meds}}<tr><td style="padding:8px 0;border-top:1px solid #eff0f1;">{{.Name}}</td><td align="right" style="padding:8px 0;border-top:1px solid #eff0f1;">{{.Result}}</td></tr>{{end}}
</table></td></tr>
{{if .Low}}<tr><td style="padding:12px 32px 0;"><p style="margin:0;padding:12px 16px;background:#fde7e6;color:#8c1d18;border-radius:12px;font-size:14px;line-height:1.5;">Stock bajo: {{range $i, $n := .Low}}{{if $i}}, {{end}}{{$n}}{{end}}. Acordate de reponerlo.</p></td></tr>{{end}}
<tr><td align="center" style="padding:24px 32px;"><table role="presentation" cellpadding="0" cellspacing="0"><tr><td align="center" bgcolor="#3646ab" style="border-radius:12px;"><a href="{{.AppURL}}" style="display:inline-block;padding:14px 28px;font-size:16px;font-weight:bold;color:#ffffff;text-decoration:none;border-radius:12px;">Abrir MediPlan</a></td></tr></table></td></tr>
<tr><td style="padding:0 32px 28px;"><p style="margin:0;font-size:12px;line-height:1.5;color:#6b7280;">Recibís este resumen porque lo activaste en tu perfil de MediPlan Premium. Podés desactivarlo cuando quieras desde ahí. Es informativo y no reemplaza la consulta con tu médico.</p></td></tr>
</table></td></tr></table></body></html>`))

// Render arma el correo (HTML + texto plano). El destinatario lo completa quien lo envía.
func Render(w Week, summary, webBase string, loc *time.Location, now time.Time) accounts.Message {
	v := view{Title: "Tu resumen semanal", Summary: summary, AppURL: webBase + "/inicio/", LogoURL: webBase + "/brand/email-logo.png",
		Range: fmt.Sprintf("Del %s al %s", now.Add(-7*24*time.Hour).In(loc).Format("02/01"), now.In(loc).Format("02/01"))}
	text := v.Title + " (" + v.Range + ")\n\n" + summary + "\n\n"
	for _, m := range w.Meds {
		res := fmt.Sprintf("%d tomadas, %d omitidas", m.Taken, m.Missed)
		v.Meds = append(v.Meds, medView{m.Name, res})
		text += "- " + m.Name + ": " + res + "\n"
		if m.LowStock {
			v.Low = append(v.Low, m.Name)
		}
	}
	if len(v.Low) > 0 {
		text += "\nStock bajo: "
		for i, n := range v.Low {
			if i > 0 {
				text += ", "
			}
			text += n
		}
		text += ".\n"
	}
	text += "\nAbrir MediPlan: " + v.AppURL + "\n"
	var b bytes.Buffer
	_ = layout.Execute(&b, v)
	return accounts.Message{Subject: "Tu resumen semanal de MediPlan", HTML: b.String(), Text: text}
}
