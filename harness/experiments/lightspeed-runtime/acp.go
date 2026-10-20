package main

// Passive ACP experiment over the existing harness connection and tee.
// The Python process owns the Lightspeed-built graph; Go owns lifecycle/Git.
// This is deliberately outside the production migration-harness entrypoint.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/konveyor/migration-harness/internal/acp"
	"github.com/konveyor/migration-harness/internal/tee"
)

func runACPDriver(parent context.Context, cfg *settings, input driverInput) (*driverResult, error) {
	key := os.Getenv("KONVEYOR_ACP_SECRET_KEY")
	if key == "" || os.Getenv("HARNESS_HITL_STEER") != "off" || os.Getenv("HARNESS_HITL_ASK") != "off" {
		return nil, errors.New("passive demo requires ACP authentication and explicit watch-only policy")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, errors.New("cannot encode ACP runtime configuration")
	}
	ctx, cancel := context.WithTimeout(parent, childTimeout)
	cmd := execCommandContext(ctx, "python3", "/opt/runtime-spike/acp_runtime_server.py")
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Stderr = os.Stderr
	cmd.Env = runtimeEnvironment(cfg)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, errors.New("cannot open ACP runtime result pipe")
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, errors.New("ACP runtime launch failed")
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	type resultRecord struct {
		result *driverResult
		err    error
	}
	resultCh := make(chan resultRecord, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), maxDriverOutput)
		if !scanner.Scan() {
			resultCh <- resultRecord{err: errors.New("ACP runtime returned no terminal record")}
			return
		}
		r, err := decodeDriverResult(scanner.Bytes())
		resultCh <- resultRecord{result: r, err: err}
	}()
	var ws *acp.WSClient
	var endpoint *tee.Server
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if endpoint != nil {
				endpoint.Stop()
			}
			if ws != nil {
				_ = ws.Close()
			}
			cancel()
			select {
			case <-exited:
			case <-time.After(6 * time.Second):
				_ = cmd.Process.Kill()
			}
		})
	}
	cfg.cleanupRuntime = cleanup
	keepAlive := false
	defer func() {
		if !keepAlive {
			cleanup()
		}
	}()

	readyDeadline := time.NewTimer(90 * time.Second)
	defer readyDeadline.Stop()
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
ready:
	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("ACP runtime startup timed out")
		case <-readyDeadline.C:
			return nil, errors.New("ACP runtime did not become ready")
		case <-exited:
			return nil, errors.New("ACP runtime exited before readiness")
		case <-poll.C:
			conn, err := net.DialTimeout("tcp", "127.0.0.1:4001", 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				break ready
			}
		}
	}
	ws, err = acp.NewWSClient("127.0.0.1", 4001, key)
	if err != nil {
		return nil, errors.New("ACP runtime WebSocket connection failed")
	}
	session := acp.NewSessionClient(ws)
	if _, err := session.Initialize(ctx); err != nil {
		return nil, errors.New("ACP runtime initialize failed")
	}
	sid, err := session.CreateSession(ctx, cfg.RuntimeRoot, nil)
	if err != nil {
		return nil, errors.New("ACP runtime session creation failed")
	}
	endpoint = tee.New(tee.Config{SecretKey: key, UpstreamAddr: "127.0.0.1:4001", SteerEnabled: false})
	if err := endpoint.Start(4000); err != nil {
		return nil, errors.New("ACP passive endpoint unavailable")
	}
	endpoint.AttachRun(ws, sid)
	cfg.runtimeNotice = endpoint.EmitRunNotice
	endpoint.SetRunActive(true)
	endpoint.EmitRunNotice("Shared Lightspeed runtime ready; passive observation enabled")
	fmt.Fprintln(os.Stderr, "passive ACP ready; root=/workspace; steering=off; ask_user=off")
	// Give the bounded external observer time to attach before the first tool.
	select {
	case <-ctx.Done():
		return nil, errors.New("ACP run cancelled before prompt")
	case <-time.After(3 * time.Second):
	}
	primary, promptErr := session.SendPrompt(ctx, sid, []acp.ContentBlock{{Type: "text", Text: input.Prompt}}, 0)
	endpoint.SetRunActive(false)
	if promptErr != nil || primary == nil || primary.StopReason != "end_turn" {
		return nil, errors.New("ACP prompt did not complete successfully")
	}
	select {
	case record := <-resultCh:
		if record.err != nil {
			return nil, record.err
		}
		keepAlive = true
		return record.result, nil
	case <-ctx.Done():
		return nil, errors.New("ACP runtime terminal record timed out")
	case <-time.After(10 * time.Second):
		return nil, errors.New("ACP runtime terminal record missing")
	}
}
