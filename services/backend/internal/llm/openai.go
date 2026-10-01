package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
)

type envelope struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Model  string `json:"model"`
	Usage  Usage  `json:"usage"`
	Output []struct {
		ToolCall
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (c *Client) openAI(ctx context.Context, route route, in Request) (Response, error) {
	if in.Scope != nil || in.OnProgress != nil {
		return Response{}, ErrIncomplete
	}
	input := []any{}
	if in.Messages == nil {
		content, ok := in.Data.(string)
		if !ok {
			data, err := json.Marshal(in.Data)
			if err != nil {
				return Response{}, ErrIncomplete
			}
			content = string(data)
		}
		input = append(input, map[string]string{"role": "system", "content": in.Instructions}, map[string]string{"role": "user", "content": content})
	} else {
		for _, m := range in.Messages {
			switch {
			case m.Call != nil && m.Result == nil && m.Role == "" && m.Content == "":
				input = append(input, map[string]string{"type": "function_call", "call_id": m.Call.CallID, "name": m.Call.Name, "arguments": m.Call.Arguments})
			case m.Result != nil && m.Call == nil && m.Role == "" && m.Content == "":
				input = append(input, map[string]string{"type": "function_call_output", "call_id": m.Result.CallID, "output": m.Result.Output})
			case m.Call == nil && m.Result == nil && (m.Role == "system" || m.Role == "user" || m.Role == "assistant"):
				input = append(input, map[string]string{"role": m.Role, "content": m.Content})
			default:
				return Response{}, ErrIncomplete
			}
		}
	}
	payload := map[string]any{"model": route.Model, "store": false, "max_output_tokens": in.MaxOutputTokens, "input": input}
	if route.Effort != "" {
		payload["reasoning"] = map[string]string{"effort": route.Effort}
	}
	if in.Schema != nil {
		payload["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": in.Schema.Name, "strict": true, "schema": in.Schema.Definition}}
	}
	if in.Tools != nil {
		definitions := make([]map[string]any, 0, len(in.Tools))
		for _, tool := range in.Tools {
			definitions = append(definitions, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters, "strict": tool.Strict})
		}
		payload["tools"], payload["tool_choice"], payload["parallel_tool_calls"] = definitions, in.ToolChoice, in.ParallelToolCalls
	}
	if in.OnText != nil {
		payload["stream"] = true
	}
	endpoint := route.URL
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/responses"
	}
	res, err := c.post(ctx, endpoint, route.Token, payload)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	if in.OnText != nil {
		result, err := readStream(res.Body, route.Model, in)
		result.ReasoningEffort, result.AuthMode = route.Effort, "api_key"
		return result, err
	}
	raw, err := boundedRead(res.Body, in.MaxResponseBytes)
	if err != nil {
		return Response{}, err
	}
	var wire envelope
	if json.Unmarshal(raw, &wire) != nil || wire.Status != "completed" {
		return Response{}, ErrIncomplete
	}
	result := Response{ID: wire.ID, Provider: OpenAI, Model: route.Model, ReasoningEffort: route.Effort, AuthMode: "api_key", Usage: wire.Usage, ModelCalls: 1}
	if wire.Model != "" {
		result.Model = wire.Model
	}
	for _, item := range wire.Output {
		if item.Type == "function_call" {
			result.ToolCalls = append(result.ToolCalls, item.ToolCall)
		}
		for _, content := range item.Content {
			switch content.Type {
			case "refusal":
				return Response{}, ErrRefused
			case "output_text":
				result.Text += content.Text
			}
		}
	}
	return result, nil
}

func readStream(body io.Reader, model string, in Request) (Response, error) {
	result := Response{Provider: OpenAI, Model: model, ModelCalls: 1}
	reader := &io.LimitedReader{R: body, N: in.MaxResponseBytes + 1}
	events := bufio.NewScanner(reader)
	events.Buffer(make([]byte, 0, 64<<10), int(in.MaxResponseBytes)+1)
	complete, refused := false, false
	var text strings.Builder
	for events.Scan() {
		data, ok := strings.CutPrefix(events.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var event struct {
			Type     string   `json:"type"`
			Delta    string   `json:"delta"`
			Response envelope `json:"response"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			return Response{}, ErrIncomplete
		}
		switch event.Type {
		case "response.output_text.delta":
			if complete {
				return Response{}, ErrIncomplete
			}
			text.WriteString(event.Delta)
			if int64(text.Len()) > in.MaxResponseBytes {
				return Response{}, ErrLimit
			}
			if !in.OnText(event.Delta) {
				return Response{}, context.Canceled
			}
		case "response.refusal.delta", "response.refusal.done":
			refused = true
		case "response.completed":
			if event.Response.Status != "completed" || complete {
				return Response{}, ErrIncomplete
			}
			complete = true
			result.ID, result.Usage = event.Response.ID, event.Response.Usage
			if event.Response.Model != "" {
				result.Model = event.Response.Model
			}
		case "response.failed", "response.incomplete", "error":
			return Response{}, ErrIncomplete
		}
	}
	if reader.N == 0 {
		return Response{}, ErrLimit
	}
	if events.Err() != nil || !complete {
		return Response{}, ErrIncomplete
	}
	if refused {
		return Response{}, ErrRefused
	}
	result.Text = text.String()
	return result, nil
}
