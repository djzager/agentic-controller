// Research-only reconnect probe. Credentials are read from stdin; output is
// protocol metadata only. No prompts, tool arguments, or model text are saved.
package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	var cfg struct{ URL, Key, CWD string }
	if json.NewDecoder(os.Stdin).Decode(&cfg) != nil {
		panic("invalid probe configuration")
	}
	if cfg.CWD == "" {
		cfg.CWD = "/workspace/repo"
	}
	report := map[string]any{"scope": "Disconnect/reconnect and session/load on the same live Pod through localhost port-forward", "initialized": false}
	defer func() { _ = json.NewEncoder(os.Stdout).Encode(report) }()
	dial := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	headers := http.Header{"X-Secret-Key": []string{cfg.Key}}
	connect := func() *websocket.Conn {
		c, _, err := dial.Dial(cfg.URL, headers)
		if err != nil {
			report["error"] = "authenticated connection failed"
			return nil
		}
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Minute))
		if c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}, "clientInfo": map[string]any{"name": "konveyor-reconnect-probe", "version": "poc"}}}) != nil {
			report["error"] = "initialize send failed"
			c.Close()
			return nil
		}
		return c
	}
	read := func(c *websocket.Conn) (map[string]any, bool) {
		_, data, err := c.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); ok {
				report["closeCode"] = closeErr.Code
			} else {
				report["readEnded"] = "transport closure or deadline"
			}
			return nil, false
		}
		var frame map[string]any
		if json.Unmarshal(data, &frame) != nil {
			report["error"] = "non-JSON frame"
			return nil, false
		}
		return frame, true
	}
	update := func(f map[string]any) (string, string, string) {
		if f["method"] != "session/update" {
			return "", "", ""
		}
		p, _ := f["params"].(map[string]any)
		u, _ := p["update"].(map[string]any)
		sid, _ := p["sessionId"].(string)
		kind, _ := u["sessionUpdate"].(string)
		tool, _ := u["toolCallId"].(string)
		return sid, kind, tool
	}
	c := connect()
	if c == nil {
		return
	}
	var session string
	firstTools := map[string]bool{}
	completedTools := map[string]bool{}
	for {
		f, ok := read(c)
		if !ok {
			c.Close()
			report["error"] = "connection ended before disconnect trigger"
			return
		}
		if f["id"] == float64(1) && f["result"] != nil {
			report["initialized"] = true
		}
		sid, kind, tool := update(f)
		if kind == "tool_call" {
			firstTools[tool] = true
		}
		if kind == "tool_call_update" && !strings.HasPrefix(tool, "harness-push-") {
			p, _ := f["params"].(map[string]any)
			u, _ := p["update"].(map[string]any)
			if u["status"] == "completed" {
				completedTools[tool] = true
			}
		}
		// Wait until a completed runtime tool exists, then disconnect during
		// a subsequent tool. A just-started call need not be persisted yet.
		if report["initialized"] == true && len(completedTools) > 0 && kind == "tool_call" && len(tool) > 0 && !strings.HasPrefix(tool, "harness-push-") {
			session = sid
			report["disconnectTriggeredBy"] = "runtime tool_call after a completed runtime tool"
			report["completedToolsBeforeDisconnect"] = len(completedTools)
			break
		}
	}
	report["sessionID"] = session
	report["disconnectedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	_ = c.Close() // Abrupt viewer disconnect, without cancelling the run.
	time.Sleep(2 * time.Second)
	c = connect()
	if c == nil {
		return
	}
	defer c.Close()
	report["reconnectedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	for {
		f, ok := read(c)
		if !ok {
			report["error"] = "reconnect ended before initialize"
			return
		}
		if f["id"] == float64(1) {
			if f["result"] == nil {
				report["error"] = "reconnect initialize rejected"
				return
			}
			report["reinitialized"] = true
			break
		}
	}
	if c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/load", "params": map[string]any{"sessionId": session, "cwd": cfg.CWD, "mcpServers": []any{}}}) != nil {
		report["error"] = "session/load send failed"
		return
	}
	loadUpdates, laterUpdates := map[string]int{}, map[string]int{}
	loadedTools := map[string]bool{}
	report["duringLoadUpdates"], report["afterLoadUpdates"] = loadUpdates, laterUpdates
	loadDone := false
	for {
		f, ok := read(c)
		if !ok {
			break
		}
		if f["id"] == float64(2) {
			if f["error"] != nil {
				// Error text may contain runtime/provider material; retain code only.
				e, _ := f["error"].(map[string]any)
				report["loadErrorCode"] = e["code"]
				report["error"] = "session/load rejected"
				return
			}
			if f["result"] != nil {
				report["loadedSameSession"] = true
				loadDone = true
				report["loadedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
		sid, kind, tool := update(f)
		if sid != session || kind == "" {
			continue
		}
		if loadDone {
			laterUpdates[kind]++
			if kind == "tool_call" && !loadedTools[tool] && !firstTools[tool] && !strings.HasPrefix(tool, "harness-push-") {
				report["newToolAfterLoad"] = true
			}
		} else {
			loadUpdates[kind]++
			if kind == "tool_call" {
				loadedTools[tool] = true
			}
			if kind == "tool_call_update" && completedTools[tool] {
				report["priorCompletedToolReplayed"] = true
			}
		}
	}
	report["historyReplayed"] = loadUpdates["user_message_chunk"] > 0 && loadUpdates["tool_call"] > 0 && report["priorCompletedToolReplayed"] == true
	report["observedRunStream"] = report["loadedSameSession"] == true && report["historyReplayed"] == true && laterUpdates["tool_call_update"] > 0
	if report["observedRunStream"] != true {
		report["error"] = "same-session load, replay or subsequent tool update missing"
	}
}
