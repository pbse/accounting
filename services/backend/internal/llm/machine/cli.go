package machine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"close.local/backend/internal/llm"
)

func command(agent, model, base, token, dir string, budget int) ([]string, []string) {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, "LANG=C.UTF-8"}
	if agent == "claude" {
		config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"records": map[string]any{"type": "http", "url": base + "/mcp", "headers": map[string]string{"Authorization": "Bearer " + token}}}})
		return []string{"--bare", "--restricted", "-p", "--output-format", "json", "--no-session-persistence", "--tools", "", "--disable-slash-commands", "--strict-mcp-config", "--mcp-config", string(config), "--allowedTools", "mcp__records__list_closes,mcp__records__read_evidence", "--max-turns", strconv.Itoa(budget), "--model", model, "--setting-sources", ""},
			append(env, "ANTHROPIC_BASE_URL="+base, "ANTHROPIC_API_KEY="+token, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	}
	args := []string{"exec", "--json", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--strict-config", "--skip-git-repo-check", "--sandbox", "read-only", "-m", model}
	for _, setting := range []string{
		`model_provider="context_machine"`,
		`model_providers.context_machine.name="Context machine"`,
		"model_providers.context_machine.base_url=" + strconv.Quote(base+"/v1"),
		`model_providers.context_machine.env_key="CONTEXT_MACHINE_RUN_TOKEN"`,
		`model_providers.context_machine.wire_api="responses"`,
		"model_providers.context_machine.request_max_retries=0",
		"model_providers.context_machine.stream_max_retries=0",
		"model_providers.context_machine.supports_websockets=false",
		"features.shell_tool=false", "features.unified_exec=false",
		"features.remote_plugin=false", `web_search="disabled"`,
		"mcp_servers.records.url=" + strconv.Quote(base+"/mcp"),
		`mcp_servers.records.bearer_token_env_var="CONTEXT_MACHINE_RUN_TOKEN"`,
		`mcp_servers.records.enabled_tools=["list_closes","read_evidence"]`,
		"mcp_servers.records.required=true",
		`mcp_servers.records.tools.list_closes.approval_mode="auto"`,
		`mcp_servers.records.tools.read_evidence.approval_mode="auto"`,
	} {
		args = append(args, "-c", setting)
	}
	return append(args, "-"), append(env, "CODEX_HOME="+dir, "CONTEXT_MACHINE_RUN_TOKEN="+token)
}

func parseOutput(agent string, raw []byte) (string, llm.Usage, error) {
	if agent == "claude" {
		var result struct {
			Type, Subtype, Result string
			IsError               bool `json:"is_error"`
			Usage                 llm.Usage
		}
		if json.Unmarshal(raw, &result) != nil || result.Type != "result" || result.Subtype != "success" || result.IsError || strings.TrimSpace(result.Result) == "" {
			return "", llm.Usage{}, llm.ErrIncomplete
		}
		return result.Result, result.Usage, nil
	}
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 1<<20)
	var text string
	var usage llm.Usage
	completed := false
	for scan.Scan() {
		var event struct {
			Type  string
			Item  struct{ Type, Text string }
			Usage llm.Usage
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil {
			return "", usage, llm.ErrIncomplete
		}
		switch event.Type {
		case "item.completed":
			if event.Item.Type == "agent_message" {
				text = event.Item.Text
			}
		case "turn.completed":
			usage.Input += event.Usage.Input
			usage.Output += event.Usage.Output
			completed = true
		case "turn.failed", "error":
			return "", usage, errors.New("agent failed")
		}
	}
	if scan.Err() != nil || !completed || strings.TrimSpace(text) == "" {
		return "", usage, llm.ErrIncomplete
	}
	return text, usage, nil
}
