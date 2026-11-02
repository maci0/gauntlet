// Copyright (C) 2026 Marcel W. Wysocki
// SPDX-License-Identifier: AGPL-3.0-or-later

package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/maci0/gauntlet/internal/agent"
	"github.com/maci0/gauntlet/internal/gauntlethome"
	"github.com/maci0/gauntlet/internal/normalize"
	"github.com/maci0/gauntlet/internal/runx"
)

func withACP(ctx context.Context, o procOpts, spec agent.Spec, prompt string, continuing bool, resume string) (selected procOpts) {
	original := o
	defer func() {
		if continuing && resume != "" && selected.ACPPrompt == nil && selected.ACPPreflightErr == nil {
			selected.ACPPreflightErr = errors.New("cannot continue ACP session: agent ACP mode is unavailable")
		}
		if selected.ACPPrompt != nil && !continuing {
			selected.ACPFallbackArgv = original.Argv
			selected.ACPFallbackStream = original.Stream
		}
	}()
	if len(o.Argv) == 0 {
		return o
	}
	if spec.Tool == "kimi" && o.Argv[0] == "kimi" {
		if _, custom := agent.CustomDef(spec.Tool); !custom && archivedKimi(ctx, o.Argv[0], o.Dir) {
			o.ACPPreflightErr = errArchivedKimi
			return o
		}
	}
	if continuing && resume == "" {
		return o
	}
	if def, ok := agent.CustomDef(spec.Tool); ok {
		if len(def.ACP) != 0 {
			executable, err := gauntlethome.ExpandPath(def.Argv[0])
			server := def.ACP[0]
			if !filepath.IsAbs(server) && strings.ContainsRune(server, filepath.Separator) {
				server = filepath.Join(o.Dir, server)
			}
			if err == nil && o.Argv[0] == executable && (continuing || runx.LookPath(server) != "") {
				o.Argv = slices.Clone(def.ACP)
				o.ACPPrompt, o.ACPModel, o.ACPEffort, o.ACPResume = &prompt, spec.Model, spec.Effort, resume
				o.Stream = false
			}
		} else if spec.Tool == "copilot" && o.Argv[0] == "copilot" && spec.Effort == "" && spec.Model != "auto" &&
			slices.Equal(def.Argv, []string{"copilot", "--allow-all", "-p", "{prompt}"}) &&
			slices.Equal(def.Model, []string{"--model", "{model}"}) &&
			slices.Equal(def.Effort, []string{"--reasoning-effort", "{effort}"}) &&
			len(def.Stream) == 0 && slices.Equal(def.Continue, []string{"--continue"}) && acpFlag(ctx, o.Argv[0], o.Dir) == "--acp" {
			o.Argv = []string{"copilot", "--acp", "--allow-all"}
			o.ACPPrompt, o.ACPModel, o.ACPResume, o.Stream = &prompt, spec.Model, resume, false
			o.ACPFallbackAuth = true
		}
		return o
	}
	// A --bin override may be a wrapper that treats every invocation as work.
	// Only probe the CLI selected by its standard name.
	if spec.Tool != "opencode" && o.Argv[0] != spec.Tool {
		return o
	}
	adapterEffort := spec.Effort == "" || (spec.Tool == "claude" && slices.Contains([]string{"low", "medium", "high", "max"}, spec.Effort))
	if (spec.Tool == "codex" || spec.Tool == "claude") && adapterEffort {
		adapter := map[string]string{"codex": "codex-acp", "claude": "claude-agent-acp"}[spec.Tool]
		binary, executable := runx.LookPath(adapter), runx.LookPath(spec.Tool)
		if binary != "" && executable != "" {
			o.Argv = []string{binary}
			o.ACPPrompt, o.ACPResume = &prompt, resume
			o.ACPModel, o.ACPEffort = spec.Model, spec.Effort
			if spec.Tool == "codex" {
				o.ACPMode, o.ACPEnv = "agent-full-access", []string{"CODEX_PATH=" + executable}
			} else {
				o.ACPMode, o.ACPEnv = "bypassPermissions", []string{"CLAUDE_CODE_EXECUTABLE=" + executable}
			}
			o.Stream = false
			return o
		}
	}
	use := spec.Tool == "opencode" && slices.Contains(o.Argv, "--standalone") && subcommandACP(ctx, o.Argv[0], o.Dir)
	acpModel, acpEffort := spec.Model, spec.Effort
	if use && strings.Contains(spec.Model, "#") {
		model, variant, _ := strings.Cut(spec.Model, "#")
		if model == "" || variant == "" || strings.Contains(variant, "#") || spec.Effort != "" {
			use = false
		} else {
			acpModel, acpEffort = model, variant
		}
	}
	arg := "acp"
	if spec.Tool == "kimi" && spec.Effort == "" {
		use = subcommandACP(ctx, o.Argv[0], o.Dir)
		if !use && spec.Model == "" {
			arg = acpFlag(ctx, o.Argv[0], o.Dir)
			use = arg != ""
		}
	}
	if spec.Tool == "cursor-agent" && spec.Effort == "" && subcommandACP(ctx, o.Argv[0], o.Dir) {
		binary := o.Argv[0]
		o.Argv = []string{binary}
		if spec.Model != "" {
			o.Argv = append(o.Argv, "--model", spec.Model)
		}
		o.Argv = append(o.Argv, "acp")
		o.ACPPrompt, o.ACPResume, o.Stream = &prompt, resume, false
		return o
	}
	if spec.Tool == "grok" && spec.Effort == "" && grokACP(ctx, o.Argv[0], o.Dir) {
		o.Argv = []string{o.Argv[0], "agent", "--no-leader", "--always-approve"}
		if spec.Model != "" {
			o.Argv = append(o.Argv, "--model", spec.Model)
		}
		o.Argv = append(o.Argv, "stdio")
		o.ACPPrompt, o.ACPModel, o.ACPResume, o.Stream = &prompt, spec.Model, resume, false
		return o
	}
	if spec.Tool == "dsh" && spec.Effort == "" {
		provider, model, explicit := strings.Cut(spec.Model, "/")
		if !explicit {
			model = provider
			provider = ""
			if model != "" {
				provider, _ = agent.DshDefaultProvider(o.Argv[0], o.Now)
			}
		}
		if provider != "" && model != "" && dshACP(ctx, o.Argv[0], o.Dir) {
			encoded, _ := json.Marshal([]string{provider, model})
			o.Argv = []string{o.Argv[0], "--profile", "acp"}
			o.ACPPrompt, o.ACPModel, o.ACPResume, o.Stream = &prompt, string(encoded), resume, false
			o.ACPFallbackInvalidModel = true
			return o
		}
	}
	if (spec.Tool == "gemini" || spec.Tool == "qwen") && spec.Effort == "" {
		arg = acpFlag(ctx, o.Argv[0], o.Dir)
		use = arg != ""
	}
	if !use {
		return o
	}
	o.Argv = []string{o.Argv[0], arg}
	o.ACPPrompt, o.ACPResume = &prompt, resume
	if spec.Tool == "opencode" || spec.Tool == "kimi" {
		o.ACPModel, o.ACPEffort = acpModel, acpEffort
		o.ACPRejectKimiCode = spec.Tool == "kimi"
	} else if spec.Model != "" {
		o.Argv = append(o.Argv, "-m", spec.Model)
	}
	o.Stream = false
	return o
}

var errACPConfigUnsupported = errors.New("ACP configuration is not supported")
var errACPInvalidModel = errors.New("ACP model selection is invalid")
var errACPResumeUnsupported = errors.New("ACP session resume is not supported")
var errACPAuthenticationRequired = errors.New("ACP authentication is required")
var errACPProtocolUnsupported = errors.New("ACP protocol is not supported")
var errACPAgentUnsupported = errors.New("ACP agent cannot reliably report failed turns")
var errACPPromptFailed = errors.New("ACP prompt failed")
var errArchivedKimi = errors.New("kimi-cli 1.52.0 no longer runs prompts; install a working Kimi CLI")

func archivedKimi(parent context.Context, binary, dir string) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Dir, cmd.Env = dir, runx.EnvIn(dir)
	out, _ := runx.Bound(cmd, 64<<10, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if cmd.Run() != nil {
		return false
	}
	line, _, _ := strings.Cut(out.String(), "\n")
	return strings.TrimSpace(line) == "kimi, version 1.52.0"
}

func acpErrorText(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return normalize.Truncate(runx.FirstLine(normalize.Display(line)), streamLineCols)
}

// dshACP detects the automation profile without invoking bunx or a wrapper.
func dshACP(parent context.Context, binary, dir string) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--profile", "acp", "--help")
	cmd.Dir, cmd.Env = dir, runx.EnvIn(dir)
	out, errOut := runx.Bound(cmd, 64<<10, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	return cmd.Run() == nil && strings.Contains(out.String()+"\n"+errOut.String(), "Agent Client Protocol")
}

// grokACP requires the local-only and approval flags as well as stdio support.
func grokACP(parent context.Context, binary, dir string) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "agent", "--help")
	cmd.Dir, cmd.Env = dir, runx.EnvIn(dir)
	out, errOut := runx.Bound(cmd, 64<<10, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if cmd.Run() != nil {
		return false
	}
	help := out.String() + "\n" + errOut.String()
	return strings.Contains(help, "stdio") && strings.Contains(help, "--no-leader") && strings.Contains(help, "--always-approve")
}

// subcommandACP detects the native subcommand without assuming all versions have it.
func subcommandACP(parent context.Context, binary, dir string) bool {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "acp", "--help")
	cmd.Dir = dir
	cmd.Env = runx.EnvIn(dir)
	out, errOut := runx.Bound(cmd, 64<<10, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	return cmd.Run() == nil && strings.Contains(out.String()+"\n"+errOut.String(), "Agent Client Protocol")
}

// acpFlag selects a documented ACP flag only when the installed CLI lists it.
func acpFlag(parent context.Context, binary, dir string) string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--help")
	cmd.Dir = dir
	cmd.Env = runx.EnvIn(dir)
	out, errOut := runx.Bound(cmd, 64<<10, runx.WaitGrace)
	defer runx.KillGroup(cmd, syscall.SIGKILL)
	if cmd.Run() != nil {
		return ""
	}
	help := out.String() + "\n" + errOut.String()
	for field := range strings.FieldsSeq(help) {
		if strings.TrimRight(field, ",") == "--acp" {
			return "--acp"
		}
	}
	for field := range strings.FieldsSeq(help) {
		if strings.TrimRight(field, ",") == "--experimental-acp" {
			return "--experimental-acp"
		}
	}
	return ""
}

// acpSession exchanges newline-delimited JSON-RPC with one agent process.
// The caller owns the process, its deadline, and both streams.
func acpSession(in io.Reader, out io.Writer, dir, prompt, model, effort, resume, mode string, allow, requireResume bool, text, progress func(string), usage func(agent.Usage), sessionID func(string), rejectKimiCode ...bool) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	encoder := json.NewEncoder(out)
	request := func(id int, method string, params any) error {
		return encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	}
	if err := request(1, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": map[string]string{"name": "gauntlet", "version": "1"}}); err != nil {
		return err
	}
	stage := 1
	session := ""
	sessionMethod := "session/new"
	sessionParams := map[string]any{"cwd": dir, "mcpServers": []any{}}
	authMethod, triedAuthentication := "", false
	legacyModeAdvertised, legacyModelAdvertised := false, false
	confirmedMode, confirmedModel, confirmedEffort := false, false, false
	advance := func() error {
		params := map[string]any{"sessionId": session}
		if stage == 3 || stage == 4 || stage == 6 {
			if stage == 3 {
				params["configId"], params["value"] = "model", model
			} else if stage == 4 {
				params["configId"], params["value"] = "effort", effort
			} else {
				params["configId"], params["value"] = "mode", mode
			}
			return request(stage, "session/set_config_option", params)
		}
		params["prompt"] = []any{map[string]string{"type": "text", "text": prompt}}
		return request(stage, "session/prompt", params)
	}
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int             `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return fmt.Errorf("invalid ACP message: %w", err)
		}
		if msg.Method == "session/update" {
			var event struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					Kind         string                     `json:"sessionUpdate"`
					Title        string                     `json:"title"`
					Meta         map[string]json.RawMessage `json:"_meta"`
					CurrentModel string                     `json:"currentModelId"`
					CurrentMode  string                     `json:"currentModeId"`
					Options      []struct {
						ID      string `json:"id"`
						Current string `json:"currentValue"`
					} `json:"configOptions"`
					Content struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"update"`
			}
			if json.Unmarshal(msg.Params, &event) == nil && event.SessionID == session && session != "" {
				if (stage == 9 || confirmedModel) && event.Update.Kind == "current_model_update" && event.Update.CurrentModel != model {
					return errors.New("ACP agent reported a different model")
				}
				if confirmedMode && event.Update.Kind == "current_mode_update" && event.Update.CurrentMode != mode {
					return errors.New("ACP agent reported a different mode")
				}
				if event.Update.Kind == "config_option_update" {
					for _, option := range event.Update.Options {
						if (option.ID == "mode" && confirmedMode && option.Current != mode) ||
							(option.ID == "model" && confirmedModel && option.Current != model) ||
							(option.ID == "effort" && confirmedEffort && option.Current != effort) {
							return fmt.Errorf("ACP agent changed the selected %s", option.ID)
						}
					}
				}
				_, child := event.Update.Meta["opencode/child-session"]
				if event.Update.Kind == "agent_message_chunk" && event.Update.Content.Type == "text" && text != nil && !child {
					text(event.Update.Content.Text)
				} else if event.Update.Kind == "tool_call" && event.Update.Title != "" && progress != nil {
					progress(event.Update.Title)
				}
			}
			continue
		}
		if msg.Method != "" && len(msg.ID) != 0 {
			response := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
			if msg.Method == "session/request_permission" {
				var p struct {
					SessionID string `json:"sessionId"`
					Options   []struct {
						ID   string `json:"optionId"`
						Kind string `json:"kind"`
					} `json:"options"`
				}
				_ = json.Unmarshal(msg.Params, &p)
				outcome := map[string]string{"outcome": "cancelled"}
				if allow && session != "" && p.SessionID == session {
					for _, option := range p.Options {
						if option.Kind == "allow_once" && option.ID != "" {
							outcome = map[string]string{"outcome": "selected", "optionId": option.ID}
							break
						}
					}
				}
				response["result"] = map[string]any{"outcome": outcome}
			} else {
				response["error"] = map[string]any{"code": -32601, "message": "method not supported"}
			}
			if err := encoder.Encode(response); err != nil {
				return err
			}
			continue
		}
		if string(msg.ID) != fmt.Sprint(stage) {
			continue
		}
		if msg.Error != nil {
			if stage == 1 && msg.Error.Code == -32601 {
				return fmt.Errorf("%w: initialize method unavailable", errACPProtocolUnsupported)
			}
			if stage == 2 && msg.Error.Code == -32000 && authMethod != "" && !triedAuthentication {
				triedAuthentication = true
				stage = 7
				if err := request(stage, "authenticate", map[string]string{"methodId": authMethod}); err != nil {
					return err
				}
				continue
			}
			if (stage == 2 || stage == 7) && msg.Error.Code == -32000 {
				message := msg.Error.Message
				var detail struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(msg.Error.Data, &detail) == nil && detail.Message != "" {
					message = detail.Message
				} else {
					var text string
					if json.Unmarshal(msg.Error.Data, &text) == nil && text != "" {
						message = text
					}
				}
				return fmt.Errorf("%w: %s", errACPAuthenticationRequired, acpErrorText(message))
			}
			if stage == 6 && msg.Error.Code == -32601 && legacyModeAdvertised {
				stage = 8
				if err := request(stage, "session/set_mode", map[string]string{"sessionId": session, "modeId": mode}); err != nil {
					return err
				}
				continue
			}
			if stage == 3 && msg.Error.Code == -32601 && legacyModelAdvertised {
				stage = 9
				if err := request(stage, "session/set_model", map[string]string{"sessionId": session, "modelId": model}); err != nil {
					return err
				}
				continue
			}
			if (stage == 3 || stage == 4 || stage == 6 || stage == 8 || stage == 9) && msg.Error.Code == -32601 {
				return fmt.Errorf("%w: %s", errACPConfigUnsupported, acpErrorText(msg.Error.Message))
			}
			if stage == 3 && msg.Error.Code == -32602 {
				return fmt.Errorf("%w: %s", errACPInvalidModel, acpErrorText(msg.Error.Message))
			}
			if stage == 5 {
				var detail struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(msg.Error.Data, &detail) == nil && detail.Error != "" {
					return fmt.Errorf("%w (code %d): %s: %s", errACPPromptFailed, msg.Error.Code, acpErrorText(msg.Error.Message), acpErrorText(detail.Error))
				}
				return fmt.Errorf("%w (code %d): %s", errACPPromptFailed, msg.Error.Code, acpErrorText(msg.Error.Message))
			}
			return fmt.Errorf("ACP request %d failed (code %d): %s", stage, msg.Error.Code, acpErrorText(msg.Error.Message))
		}
		switch stage {
		case 1:
			var result struct {
				Version   int `json:"protocolVersion"`
				AgentInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"agentInfo"`
				AuthMethods []struct {
					ID string `json:"id"`
				} `json:"authMethods"`
				Meta struct {
					DefaultAuthMethodID string `json:"defaultAuthMethodId"`
				} `json:"_meta"`
				Capabilities struct {
					LoadSession bool `json:"loadSession"`
					MCP         struct {
						HTTP bool  `json:"http"`
						SSE  *bool `json:"sse"`
					} `json:"mcpCapabilities"`
					Sessions struct {
						Resume json.RawMessage `json:"resume"`
					} `json:"sessionCapabilities"`
				} `json:"agentCapabilities"`
			}
			if json.Unmarshal(msg.Result, &result) != nil || result.Version == 0 {
				return errors.New("invalid ACP initialization response")
			}
			if result.Version != 1 {
				return fmt.Errorf("%w: version %d", errACPProtocolUnsupported, result.Version)
			}
			// shortcut: allow only verified Python releases until Kimi Code fixes failed-turn reporting.
			pythonKimi := slices.Contains([]string{"1.49.0", "1.50.0", "1.51.0"}, result.AgentInfo.Version) && result.Capabilities.MCP.HTTP && (result.Capabilities.MCP.SSE == nil || !*result.Capabilities.MCP.SSE)
			if len(rejectKimiCode) != 0 && rejectKimiCode[0] && result.AgentInfo.Name == "Kimi Code CLI" && !pythonKimi {
				return fmt.Errorf("%w: Kimi Code", errACPAgentUnsupported)
			}
			for _, method := range result.AuthMethods {
				if (method.ID == "cached_token" && (result.Meta.DefaultAuthMethodID == "" || result.Meta.DefaultAuthMethodID == method.ID)) ||
					(method.ID == "xai.api_key" && result.Meta.DefaultAuthMethodID == method.ID) {
					authMethod = method.ID
					break
				}
			}
			var resumeCapability map[string]json.RawMessage
			canResume := json.Unmarshal(result.Capabilities.Sessions.Resume, &resumeCapability) == nil && resumeCapability != nil
			if requireResume && !canResume && !result.Capabilities.LoadSession {
				return errACPResumeUnsupported
			}
			stage = 2
			if resume != "" {
				if canResume {
					sessionMethod = "session/resume"
				} else if result.Capabilities.LoadSession {
					sessionMethod = "session/load"
				} else {
					return errors.New("ACP agent does not support session resume")
				}
				sessionParams["sessionId"] = resume
			}
			if err := request(stage, sessionMethod, sessionParams); err != nil {
				return err
			}
		case 7:
			stage = 2
			if err := request(stage, sessionMethod, sessionParams); err != nil {
				return err
			}
		case 2:
			var result struct {
				SessionID string `json:"sessionId"`
				Modes     struct {
					Available []struct {
						ID string `json:"id"`
					} `json:"availableModes"`
				} `json:"modes"`
				Models struct {
					Available []struct {
						ID string `json:"modelId"`
					} `json:"availableModels"`
				} `json:"models"`
			}
			if json.Unmarshal(msg.Result, &result) != nil {
				return errors.New("ACP session has no id")
			}
			session = result.SessionID
			if resume != "" {
				if session != "" && session != resume {
					return errors.New("ACP agent resumed a different session")
				}
				session = resume
			}
			if session == "" {
				return errors.New("ACP session has no id")
			}
			for _, available := range result.Modes.Available {
				if available.ID == mode && mode != "" {
					legacyModeAdvertised = true
					break
				}
			}
			for _, available := range result.Models.Available {
				if available.ID == model && model != "" {
					legacyModelAdvertised = true
					break
				}
			}
			if sessionID != nil {
				sessionID(session)
			}
			stage = 5
			if model != "" {
				stage = 3
			} else if effort != "" {
				stage = 4
			}
			if mode != "" {
				stage = 6
			}
			if err := advance(); err != nil {
				return err
			}
		case 8, 9:
			var result map[string]json.RawMessage
			if json.Unmarshal(msg.Result, &result) != nil || result == nil {
				return errors.New("invalid ACP legacy selection response")
			}
			selectedMode := stage == 8
			if selectedMode {
				confirmedMode = true
			} else {
				confirmedModel = true
			}
			stage = 5
			if selectedMode && model != "" {
				stage = 3
			} else if effort != "" {
				stage = 4
			}
			if err := advance(); err != nil {
				return err
			}
		case 3, 4, 6:
			var result struct {
				Options []struct {
					ID      string `json:"id"`
					Current string `json:"currentValue"`
				} `json:"configOptions"`
			}
			if err := json.Unmarshal(msg.Result, &result); err != nil {
				return fmt.Errorf("invalid ACP config response: %w", err)
			}
			name, value := "model", model
			if stage == 4 {
				name, value = "effort", effort
			} else if stage == 6 {
				name, value = "mode", mode
			}
			matched := false
			for _, option := range result.Options {
				if confirmedMode && option.ID == "mode" && option.Current != mode {
					return errors.New("ACP agent changed the selected mode")
				}
				if confirmedModel && option.ID == "model" && option.Current != model {
					return errors.New("ACP agent changed the selected model")
				}
				if option.ID == name {
					if option.Current != value {
						return fmt.Errorf("ACP agent did not select %s %q", name, value)
					}
					matched = true
				}
			}
			if !matched {
				return fmt.Errorf("ACP agent did not select %s %q", name, value)
			}
			if stage == 6 {
				confirmedMode = true
			} else if stage == 3 {
				confirmedModel = true
			} else if stage == 4 {
				confirmedEffort = true
			}
			if stage == 6 && model != "" {
				stage = 3
			} else if (stage == 3 || stage == 6) && effort != "" {
				stage = 4
			} else {
				stage = 5
			}
			if err := advance(); err != nil {
				return err
			}
		case 5:
			var result struct {
				StopReason string          `json:"stopReason"`
				Usage      json.RawMessage `json:"usage"`
			}
			if json.Unmarshal(msg.Result, &result) != nil {
				return errors.New("invalid ACP prompt response")
			}
			if usage != nil {
				var raw struct {
					Output   *int64 `json:"outputTokens"`
					Thinking *int64 `json:"thoughtTokens"`
					Total    *int64 `json:"totalTokens"`
				}
				if json.Unmarshal(result.Usage, &raw) == nil {
					count := func(n *int64) int {
						if n == nil || *n < 0 || *n > 1<<40 || uint64(*n) > uint64(^uint(0)>>1) {
							return -1
						}
						return int(*n)
					}
					usage(agent.Usage{Output: count(raw.Output), Thinking: count(raw.Thinking), Total: count(raw.Total)})
				}
			}
			if result.StopReason != "end_turn" {
				return fmt.Errorf("ACP prompt stopped: %s", acpErrorText(result.StopReason))
			}
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
