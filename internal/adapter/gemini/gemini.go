// Package gemini habla con la API generateContent de Google Gemini (clave de Google AI Studio, plan gratuito).
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lea/mediplan-api/internal/usecase/ai"
)

// maxRounds limita cuántas veces el modelo puede pedir herramientas en una misma respuesta.
const maxRounds = 4

type Client struct {
	apiKey, model string
	baseURL       string
	http          *http.Client
}

// New recibe la clave (SECRETO) y el nombre del modelo (variable de entorno: Google los renueva seguido).
func New(apiKey, model string) *Client {
	return &Client{apiKey: apiKey, model: model, baseURL: "https://generativelanguage.googleapis.com", http: &http.Client{Timeout: 40 * time.Second}}
}

type part = map[string]any
type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

// Chat envía la conversación; si el modelo pide herramientas, las ejecuta y vuelve a consultar hasta tener texto.
func (c *Client) Chat(ctx context.Context, system string, history []ai.Msg, tools []ai.Tool) (string, error) {
	var contents []content
	for _, m := range history {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, content{Role: role, Parts: []part{{"text": m.Text}}})
	}
	byName := map[string]ai.Tool{}
	var decls []map[string]any
	for _, t := range tools {
		byName[t.Name] = t
		d := map[string]any{"name": t.Name, "description": t.Description}
		if t.Params != nil {
			d["parameters"] = t.Params
		}
		decls = append(decls, d)
	}

	for round := 0; round <= maxRounds; round++ {
		body := map[string]any{"contents": contents, "generationConfig": map[string]any{"temperature": 0.4, "maxOutputTokens": 800}}
		if system != "" {
			body["systemInstruction"] = map[string]any{"parts": []part{{"text": system}}}
		}
		if len(decls) > 0 && round < maxRounds {
			body["tools"] = []map[string]any{{"functionDeclarations": decls}}
		}
		reply, err := c.generate(ctx, body)
		if err != nil {
			return "", err
		}
		var calls []part
		var text strings.Builder
		for _, p := range reply.Parts {
			if _, ok := p["functionCall"]; ok {
				calls = append(calls, p)
			} else if t, ok := p["text"].(string); ok {
				if th, _ := p["thought"].(bool); !th {
					text.WriteString(t)
				}
			}
		}
		if len(calls) == 0 {
			if strings.TrimSpace(text.String()) == "" {
				return "", errors.New("gemini: respuesta vacía")
			}
			return strings.TrimSpace(text.String()), nil
		}
		// El turno del modelo se devuelve tal cual (incluye firmas internas que la API pide conservar).
		contents = append(contents, content{Role: "model", Parts: reply.Parts})
		var results []part
		for _, call := range calls {
			fc, _ := call["functionCall"].(map[string]any)
			name, _ := fc["name"].(string)
			args, _ := fc["args"].(map[string]any)
			resp := map[string]any{}
			if t, ok := byName[name]; !ok {
				resp["error"] = "herramienta desconocida"
			} else if out, err := t.Run(ctx, args); err != nil {
				resp["error"] = "no se pudo obtener el dato"
			} else {
				resp["result"] = out
			}
			fr := map[string]any{"name": name, "response": resp}
			if id, ok := fc["id"].(string); ok && id != "" {
				fr["id"] = id
			}
			results = append(results, part{"functionResponse": fr})
		}
		contents = append(contents, content{Role: "user", Parts: results})
	}
	return "", errors.New("gemini: demasiadas rondas de herramientas")
}

type candidate struct {
	Content content `json:"content"`
}

func (c *Client) generate(ctx context.Context, body map[string]any) (*content, error) {
	raw, _ := json.Marshal(body)
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", c.baseURL, url.PathEscape(c.model))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: %s", strings.ReplaceAll(err.Error(), c.apiKey, "***"))
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		detail := string(data)
		if len(detail) > 300 {
			detail = detail[:300]
		}
		return nil, fmt.Errorf("gemini: respondió %d: %s", resp.StatusCode, detail)
	}
	var out struct {
		Candidates     []candidate `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("gemini: respuesta ilegible: %w", err)
	}
	if len(out.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: sin respuesta (bloqueo: %q)", out.PromptFeedback.BlockReason)
	}
	return &out.Candidates[0].Content, nil
}
