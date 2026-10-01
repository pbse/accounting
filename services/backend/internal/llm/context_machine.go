package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strings"
)

// contextMachine calls an independently hosted agent. The service owns the CLI,
// its tools and its read-only database credentials. None come from the caller.
func (c *Client) contextMachine(ctx context.Context, route route, in Request) (Response, error) {
	if in.Scope == nil || strings.TrimSpace(in.Scope.Organization) == "" || strings.TrimSpace(in.Scope.Subject) == "" || strings.TrimSpace(in.Scope.AccessProfile) == "" {
		return Response{}, errors.New("context machine requires an authorized access profile, organization and subject")
	}
	if strings.TrimSpace(in.Instructions) == "" || in.Messages != nil || in.Tools != nil || in.Schema != nil || in.ToolChoice != "" || in.ParallelToolCalls {
		return Response{}, errors.New("context machine supports instructions and data only; no caller tools or structured format")
	}
	if in.MaxModelCalls < 1 || in.MaxModelCalls > 16 {
		return Response{}, errors.New("context machine requires a reserved model call budget between 1 and 16")
	}
	deadline, _ := ctx.Deadline()
	body := struct {
		Version                   int    `json:"version"`
		Purpose                   string `json:"purpose"`
		Model                     string `json:"model"`
		Instructions              string `json:"instructions"`
		Data                      any    `json:"data"`
		Scope                     *Scope `json:"scope"`
		ReadOnly                  bool   `json:"read_only"`
		Store                     bool   `json:"store"`
		Deadline                  string `json:"deadline"`
		MaxOutputTokens           int    `json:"max_output_tokens"`
		MaxModelCalls             int    `json:"max_model_calls"`
		Stream                    bool   `json:"stream"`
		AllowUncappedOutputTokens bool   `json:"allow_uncapped_output_tokens,omitempty"`
	}{1, in.Purpose, route.Model, in.Instructions, in.Data, in.Scope, true, false, deadline.UTC().Format("2006-01-02T15:04:05.999999999Z"), in.MaxOutputTokens, in.MaxModelCalls, in.OnText != nil || in.OnProgress != nil, in.AllowUncappedOutputTokens}
	res, err := c.post(ctx, route.URL, route.Token, body)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	if body.Stream {
		media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if media != "application/x-ndjson" {
			return Response{}, ErrIncomplete
		}
		return readMachineStream(res.Body, in)
	}
	raw, err := boundedRead(res.Body, in.MaxResponseBytes)
	if err != nil {
		return Response{}, err
	}
	var wire machineResponse
	if json.Unmarshal(raw, &wire) != nil {
		return Response{}, ErrIncomplete
	}
	return validateMachineResponse(wire, in)
}

type machineResponse struct {
	Version int    `json:"version"`
	Status  string `json:"status"`
	Scope   Scope  `json:"scope"`
	Response
}

func validateMachineResponse(wire machineResponse, in Request) (Response, error) {
	if wire.Version != 1 || in.Scope == nil || wire.Scope != *in.Scope {
		return Response{}, ErrIncomplete
	}
	if wire.Status == "refused" {
		return Response{}, ErrRefused
	}
	if wire.Status != "completed" || wire.ID == "" || wire.Model == "" || strings.TrimSpace(wire.Text) == "" || len(wire.ToolCalls) != 0 || wire.ModelCalls < 1 || wire.ModelCalls > in.MaxModelCalls || wire.Usage.Input < 0 || wire.Usage.Output < 0 {
		return Response{}, ErrIncomplete
	}
	wire.Response.Provider = ContextMachine
	return wire.Response, nil
}

// NDJSON is internal to this adapter. Callers see the same callbacks and final
// Response if the service transport is later replaced with gRPC.
func readMachineStream(body io.Reader, in Request) (Response, error) {
	reader := &io.LimitedReader{R: body, N: in.MaxResponseBytes + 1}
	events := bufio.NewScanner(reader)
	events.Buffer(make([]byte, 0, 4096), int(in.MaxResponseBytes)+1)
	var text strings.Builder
	for events.Scan() {
		if reader.N == 0 {
			return Response{}, ErrLimit
		}
		var event struct {
			Type     string          `json:"type"`
			Scope    Scope           `json:"scope"`
			Text     string          `json:"text"`
			Response machineResponse `json:"response"`
		}
		if json.Unmarshal(events.Bytes(), &event) != nil || in.Scope == nil || event.Scope != *in.Scope {
			return Response{}, ErrIncomplete
		}
		switch event.Type {
		case "progress":
			if in.OnProgress != nil && !in.OnProgress(event.Text) {
				return Response{}, context.Canceled
			}
		case "text_delta":
			text.WriteString(event.Text)
			if in.OnText != nil && !in.OnText(event.Text) {
				return Response{}, context.Canceled
			}
		case "completed":
			result, err := validateMachineResponse(event.Response, in)
			if err != nil {
				return Response{}, err
			}
			if text.Len() > 0 && text.String() != result.Text {
				return Response{}, ErrIncomplete
			}
			return result, nil
		case "refused":
			return Response{}, ErrRefused
		case "error":
			return Response{}, ErrIncomplete
		default:
			return Response{}, ErrIncomplete
		}
	}
	if reader.N == 0 {
		return Response{}, ErrLimit
	}
	return Response{}, ErrIncomplete
}
