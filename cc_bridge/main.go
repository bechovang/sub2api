// cc-bridge: Command Code /alpha/generate translator for sub2api.
//
// sub2api is pointed at this bridge as an OpenAI-compatible upstream account
// (platform openai, type apikey). The bridge converts the incoming OpenAI
// Chat Completions request into Command Code's CLI `/alpha/generate` protocol
// (using the account's Provider *or* Go plan key) and streams back an OpenAI
// Chat Completions response.
//
// The real Command Code key is read from the env var COMMANDCODE_API_KEY and
// is NOT stored in sub2api. All Command-Code-specific quirks (custom headers,
// wrapped body, SSE event stream) are contained in this one service so sub2api
// core stays at upstream parity.
//
// Env:
//   COMMANDCODE_API_KEY   (required) the user_... key (Go or Provider plan)
//   COMMANDCODE_API_BASE  optional; default https://api.commandcode.ai
//   CC_BRIDGE_ADDR        optional; default :8788
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	cliVersion   = "1.15.1"
	defaultMax   = 60_000
	ccTimeout    = 5 * time.Minute
)

// ---- OpenAI-compatible inbound request/response ----

type openaiReq struct {
	Model            string            `json:"model"`
	Messages         []openaiMessage   `json:"messages"`
	Tools            []openaiTool      `json:"tools"`
	Stream           bool              `json:"stream"`
	MaxTokens        int               `json:"max_tokens"`
	Temperature      *float64          `json:"temperature"`
	ReasoningEffort  string            `json:"reasoning_effort"`
}

type openaiMessage struct {
	Role       string           `json:"role"`
	Content    json.RawMessage  `json:"content"` // string or list
	ToolCallID string           `json:"tool_call_id"`
	ToolCalls  []openaiToolCall `json:"tool_calls"`
}

type openaiToolCall struct {
	ID       string             `json:"id"`
	Function openaiToolCallFunc `json:"function"`
}

type openaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type openaiTool struct {
	Type     string           `json:"type"`
	Function openaiToolFunc   `json:"function"`
}

type openaiToolFunc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type toolCallOut struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ---- Command Code /alpha/generate body ----

type ccRequest struct {
	Config   ccConfig    `json:"config"`
	Memory   any         `json:"memory"`
	Taste    any         `json:"taste"`
	Skills   any         `json:"skills"`
	Params   ccParams    `json:"params"`
	ThreadID string      `json:"threadId"`
}

type ccConfig struct {
	WorkingDir    string `json:"workingDir"`
	Date          string `json:"date"`
	Environment   string `json:"environment"`
	Structure     []any  `json:"structure"`
	IsGitRepo     bool   `json:"isGitRepo"`
	CurrentBranch string `json:"currentBranch"`
	MainBranch    string `json:"mainBranch"`
	GitStatus     string `json:"gitStatus"`
	RecentCommits []any  `json:"recentCommits"`
}

type ccParams struct {
	Model           string          `json:"model"`
	Messages        []any           `json:"messages"`
	Tools           []any           `json:"tools"`
	System          string          `json:"system"`
	MaxTokens       int             `json:"max_tokens"`
	Temperature     float64         `json:"temperature"`
	Stream          bool            `json:"stream"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

// ---- Command Code SSE events ----

type ccEvent struct {
	Type string `json:"type"`
	// text
	Text string `json:"text"`
	// tool-call
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	Input      any    `json:"input"`
	// finish
	FinishReason string `json:"finishReason"`
	Usage        ccUsage `json:"totalUsage"`  // on "finish"
	StepUsage    ccUsage `json:"usage"`       // on "finish-step"
	// error
	Error   any `json:"error"`
	Message any `json:"message"`
}

type ccUsage struct {
	InputTokens    int `json:"inputTokens"`
	OutputTokens   int `json:"outputTokens"`
	CachedInput    int `json:"cachedInputTokens"`
	ReasoningTokens int `json:"reasoningTokens"`
	TotalTokens    int `json:"totalTokens"`
}

func main() {
	ccKey := strings.TrimSpace(os.Getenv("COMMANDCODE_API_KEY"))
	if ccKey == "" {
		log.Fatal("COMMANDCODE_API_KEY is required")
	}
	base := strings.TrimSpace(os.Getenv("COMMANDCODE_API_BASE"))
	if base == "" {
		base = "https://api.commandcode.ai"
	}
	addr := ":8788"
	if a := strings.TrimSpace(os.Getenv("CC_BRIDGE_ADDR")); a != "" {
		addr = a
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		handleCompletions(w, r, base, ccKey)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	log.Printf("cc-bridge listening on %s -> %s", addr, base)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func handleCompletions(w http.ResponseWriter, r *http.Request, base, ccKey string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req openaiReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		http.Error(w, "model required", http.StatusBadRequest)
		return
	}

	ccBody := buildCC(&req)

	// Call Command Code /alpha/generate (always streams at the wire level).
	reqBytes, _ := json.Marshal(ccBody)
	httpReq, err := http.NewRequest(http.MethodPost, base+"/alpha/generate", bytes.NewReader(reqBytes))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ccKey)
	httpReq.Header.Set("x-command-code-version", cliVersion)
	httpReq.Header.Set("x-cli-environment", "production")
	httpReq.Header.Set("x-project-slug", "sub2api")
	httpReq.Header.Set("x-taste-learning", "true")
	httpReq.Header.Set("x-co-flag", "false")

	client := &http.Client{Timeout: ccTimeout}
	up, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, "commandcode error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer up.Body.Close()

	if up.StatusCode != http.StatusOK {
		errText, _ := io.ReadAll(io.LimitReader(up.Body, 2048))
		http.Error(w, fmt.Sprintf("commandcode HTTP %d: %s", up.StatusCode, trunc(string(errText), 200)), http.StatusBadGateway)
		return
	}

	if req.Stream {
		writeOpenAIStream(w, req, up.Body)
		return
	}
	buf, err := buildNonStream(req, up.Body)
	if err != nil {
		http.Error(w, "commandcode stream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(buf)
}

// ---- message / tool conversion ----

func buildCC(req *openaiReq) *ccRequest {
	toolNameByID := map[string]string{}
	// Prepass: collect tool-call ids -> names from assistant messages.
	for _, m := range req.Messages {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				toolNameByID[tc.ID] = tc.Function.Name
			}
		}
	}

	var systemParts []string
	var ccMsgs []any
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemParts = append(systemParts, string(rawText(&m)))
		case "user":
			ccMsgs = append(ccMsgs, map[string]any{
				"role": "user",
				"content": []any{map[string]string{"type": "text", "text": string(rawText(&m))}},
			})
		case "assistant":
			parts := []any{}
			if t := string(rawText(&m)); t != "" {
				parts = append(parts, map[string]string{"type": "text", "text": t})
			}
			for _, tc := range m.ToolCalls {
				input := "{}"
				if strings.TrimSpace(tc.Function.Arguments) != "" {
					if json.Valid([]byte(tc.Function.Arguments)) {
						input = tc.Function.Arguments
					}
				}
				var inObj any
				_ = json.Unmarshal([]byte(input), &inObj)
				parts = append(parts, map[string]any{
					"type":        "tool-call",
					"toolCallId":  tc.ID,
					"toolName":    tc.Function.Name,
					"input":       inObj,
				})
			}
			if len(parts) > 0 {
				ccMsgs = append(ccMsgs, map[string]any{"role": "assistant", "content": parts})
			}
		case "tool":
			name := toolNameByID[m.ToolCallID]
			value := string(rawText(&m))
			ccMsgs = append(ccMsgs, map[string]any{
				"role": "tool",
				"content": []any{map[string]any{
					"type":       "tool-result",
					"toolCallId": m.ToolCallID,
					"toolName":   name,
					"output":     map[string]any{"type": "text", "value": value},
				}},
			})
		}
	}

	tools := make([]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		f := t.Function
		schema := f.Parameters
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, map[string]any{
			"type":         "function",
			"name":         f.Name,
			"description":  f.Description,
			"input_schema": schema,
		})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMax
	}
	temp := 0.3
	if req.Temperature != nil {
		temp = *req.Temperature
	}

	return &ccRequest{
		Config: ccConfig{
			WorkingDir:  "/tmp",
			Date:        time.Now().Format(time.DateOnly),
			Environment: "win32-x64, Node.js v22.0.0",
			Structure:     []any{},
			RecentCommits: []any{},
		},
		Memory: nil, Taste: nil, Skills: nil,
		Params: ccParams{
			Model:           req.Model,
			Messages:        ccMsgs,
			Tools:           tools,
			System:          strings.Join(systemParts, "\n\n"),
			MaxTokens:       maxTokens,
			Temperature:     temp,
			Stream:          true,
			ReasoningEffort: req.ReasoningEffort,
		},
		ThreadID: newUUID(),
	}
}

// rawText extracts the plain-text of an OpenAI message content (string or list of
// {type:text,text} / {type:input_text,text} parts).
func rawText(m *openaiMessage) []byte {
	if len(m.Content) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return []byte(s)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if (p.Type == "text" || p.Type == "input_text" || p.Type == "output_text") && p.Text != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			}
		}
		return []byte(b.String())
	}
	return nil
}

// ---- streaming / buffering ----

func writeOpenAIStream(w http.ResponseWriter, req openaiReq, upBody io.Reader) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// opening chunk
	send(map[string]any{
		"id": "chatcmpl-cc-" + newUUID(), "object": "chat.completion.chunk",
		"created": time.Now().Unix(), "model": req.Model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil}},
	})

	tcIndex := 0
	finalSent := false
	iterEvents(upBody, func(e ccEvent) {
		switch e.Type {
		case "text-delta":
			send(chunkFor(req, map[string]any{"content": e.Text}, nil))
		case "reasoning-delta":
			if e.Text != "" {
				send(chunkFor(req, map[string]any{"reasoning_content": e.Text}, nil))
			}
		case "tool-call":
			argsStr := "{}"
			if s, ok := e.Input.(string); ok && s != "" {
				argsStr = s
			} else if e.Input != nil {
				if b, err := json.Marshal(e.Input); err == nil {
					argsStr = string(b)
				}
			}
			tc := map[string]any{
				"index": tcIndex,
				"id":    e.ToolCallID,
				"type":  "function",
				"function": map[string]any{"name": e.ToolName, "arguments": argsStr},
			}
			tcIndex++
			send(chunkFor(req, nil, []any{tc}))
		case "finish", "finish-step":
			if finalSent {
				return
			}
			finalSent = true
			fr := mapFinish(e.FinishReason)
			u := openAIUsage(e.effectiveUsage())
			send(map[string]any{
				"id": "chatcmpl-cc-" + newUUID(), "object": "chat.completion.chunk",
				"created": time.Now().Unix(), "model": req.Model,
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": fr}},
				"usage":   u,
			})
		}
	})

	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func chunkFor(req openaiReq, delta map[string]any, toolCalls []any) map[string]any {
	choice := map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}
	if delta != nil {
		d := choice["delta"].(map[string]any)
		for k, v := range delta {
			d[k] = v
		}
	}
	if toolCalls != nil {
		choice["delta"].(map[string]any)["tool_calls"] = toolCalls
	}
	return map[string]any{
		"id": "chatcmpl-cc-" + newUUID(), "object": "chat.completion.chunk",
		"created": time.Now().Unix(), "model": req.Model,
		"choices": []any{choice},
	}
}

func buildNonStream(req openaiReq, upBody io.Reader) ([]byte, error) {
	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []toolCallOut
	finishReason := "stop"
	var usage ccUsage
	hadFinish := false

	iterEvents(upBody, func(e ccEvent) {
		switch e.Type {
		case "text-delta":
			content.WriteString(e.Text)
		case "reasoning-delta":
			reasoning.WriteString(e.Text)
		case "tool-call":
			argsStr := "{}"
			if s, ok := e.Input.(string); ok && s != "" {
				argsStr = s
			} else if e.Input != nil {
				if b, err := json.Marshal(e.Input); err == nil {
					argsStr = string(b)
				}
			}
			tc := toolCallOut{ID: e.ToolCallID, Type: "function"}
			tc.Function.Name = e.ToolName
			tc.Function.Arguments = argsStr
			toolCalls = append(toolCalls, tc)
		case "finish", "finish-step":
			if !hadFinish && e.effectiveUsage().TotalTokens > 0 {
				finishReason = mapFinish(e.FinishReason)
				usage = e.effectiveUsage()
				hadFinish = true
			}
		}
	})

	msg := map[string]any{
		"role":             "assistant",
		"content":          content.String(),
	}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}

	return json.Marshal(map[string]any{
		"id":      "chatcmpl-cc-" + newUUID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		}},
		"usage": openAIUsage(ccUsage{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CachedInput: usage.CachedInput, TotalTokens: usage.TotalTokens,
		}),
	})
}

// iterEvents reads Command Code SSE lines and dispatches text/tool/reasoning/finish.
func iterEvents(reader io.Reader, fn func(ccEvent)) error {
	sc := bufio.NewScanner(reader)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var e ccEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		switch e.Type {
		case "text-delta", "reasoning-delta", "tool-call", "finish", "finish-step":
			fn(e)
		case "error":
			fn(e)
		}
	}
	return sc.Err()
}

func (e ccEvent) effectiveUsage() ccUsage {
	u := e.Usage
	if u.TotalTokens <= 0 {
		u = e.StepUsage
	}
	return u
}

func mapFinish(reason string) string {
	switch reason {
	case "tool-calls":
		return "tool_calls"
	case "length", "max_tokens", "max-tokens", "max_output_tokens":
		return "length"
	default:
		return "stop"
	}
}

func openAIUsage(u ccUsage) map[string]any {
	prompt := u.InputTokens
	total := u.TotalTokens
	if total <= 0 {
		total = prompt + u.OutputTokens
	}
	ct := map[string]any{
		"prompt_tokens":     prompt,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      total,
	}
	_ = ct
	dp := map[string]any{
		"cached_tokens": u.CachedInput,
		"reasoning_tokens": u.ReasoningTokens,
	}
	c := map[string]any{
		"text_tokens": u.OutputTokens - u.ReasoningTokens,
		"reasoning_tokens": u.ReasoningTokens,
	}
	return map[string]any{
		"prompt_tokens":     prompt,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      total,
		"prompt_tokens_details": dp,
		"completion_tokens_details": c,
		"_cc_raw": map[string]int{
			"input": u.InputTokens, "output": u.OutputTokens,
			"cache_read": u.CachedInput, "reasoning": u.ReasoningTokens, "total": total,
		},
	}
}

func newUUID() string {
	b := make([]byte, 16)
	_ = readRand(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func readRand(b []byte) error {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.ReadFull(f, b)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	_ = n
	return nil
}