// Package ai define el puerto hacia el modelo de lenguaje (hoy Google Gemini, pero los casos de uso no lo saben).
package ai

import "context"

// Msg es un mensaje de la conversación. Role: "user" o "assistant".
type Msg struct{ Role, Text string }

// Tool es una herramienta que el modelo puede pedir. Solo de lectura: devuelve datos de la persona que consulta.
type Tool struct {
	Name        string
	Description string
	// Params es el esquema JSON de los argumentos ({"type":"object","properties":{...}}); nil = sin argumentos.
	Params map[string]any
	Run    func(ctx context.Context, args map[string]any) (any, error)
}

// LLM conversa con el modelo. Si el modelo pide herramientas, la implementación las ejecuta y le devuelve los
// resultados hasta obtener la respuesta final de texto.
type LLM interface {
	Chat(ctx context.Context, system string, history []Msg, tools []Tool) (string, error)
}
