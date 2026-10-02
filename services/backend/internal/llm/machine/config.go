package machine

import (
	"errors"
	"slices"
)

// Selection is fixed at startup. Provider errors never trigger a second run
// against another credential or billing account.
type agentSettings struct{ model, effort, mode, credential string }

func (cfg Config) agent(agent string) (agentSettings, error) {
	model, effort, mode, key, token := cfg.CodexModel, cfg.CodexEffort, cfg.CodexAuth, cfg.OpenAIKey, cfg.CodexToken
	levels := []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	if agent == "claude" {
		model, effort, mode, key, token = cfg.ClaudeModel, cfg.ClaudeEffort, cfg.ClaudeAuth, cfg.AnthropicKey, cfg.ClaudeToken
		levels = []string{"low", "medium", "high", "xhigh", "max"}
	} else if agent != "codex" {
		return agentSettings{}, errors.New("unknown agent")
	}
	if effort == "" {
		effort = "medium"
	}
	if !slices.Contains(levels, effort) {
		return agentSettings{}, errors.New("invalid agent reasoning effort")
	}
	if mode == "" {
		mode = "auto"
	}
	if !slices.Contains([]string{"auto", "api_key", "subscription", "chatgpt"}, mode) {
		return agentSettings{}, errors.New("invalid agent auth mode")
	}
	if mode == "auto" {
		if model == "" {
			return agentSettings{}, nil
		}
		if key != "" {
			mode = "api_key"
		} else if agent == "codex" && cfg.CodexAuthJSON != "" {
			mode = "chatgpt"
		} else if token != "" {
			mode = "subscription"
		} else {
			return agentSettings{}, nil
		}
	}
	credential := key
	if mode == "chatgpt" {
		if agent != "codex" {
			return agentSettings{}, errors.New("chatgpt auth is only supported by Codex")
		}
		if _, err := parseCodexAuth([]byte(cfg.CodexAuthJSON)); err != nil {
			return agentSettings{}, err
		}
		credential = cfg.CodexAuthJSON
	}
	if mode == "subscription" {
		credential = token
	}
	if credential == "" || model == "" {
		return agentSettings{}, errors.New("selected agent requires its model and credential")
	}
	return agentSettings{model, effort, mode, credential}, nil
}
