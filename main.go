// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type packet struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type bridge struct {
	output  io.Writer
	write   sync.Mutex
	waiters sync.Map
	id      atomic.Uint64
	ctx     context.Context
	cancel  context.CancelFunc
}

func newBridge(output io.Writer) *bridge {
	ctx, cancel := context.WithCancel(context.Background())
	return &bridge{output: output, ctx: ctx, cancel: cancel}
}
func (b *bridge) emit(value any) error {
	b.write.Lock()
	defer b.write.Unlock()
	if err := json.NewEncoder(b.output).Encode(value); err != nil {
		b.cancel()
		return errors.New("ipc")
	}
	return nil
}
func (b *bridge) reply(id string, result any, err error) {
	if err != nil {
		_ = b.emit(map[string]any{"id": id, "error": category(err)})
		return
	}
	_ = b.emit(map[string]any{"id": id, "result": result})
}
func (b *bridge) state(step, account, qr string) {
	_ = b.emit(map[string]any{"method": "state", "args": map[string]string{"step": step, "account": account, "qr": qr}})
}
func (b *bridge) storage(ctx context.Context, method, key string, value any, target any) error {
	id := "n" + strconv.FormatUint(b.id.Add(1), 10)
	ch := make(chan packet, 1)
	b.waiters.Store(id, ch)
	defer b.waiters.Delete(id)
	args := map[string]any{"key": key}
	if method == "store.put" {
		args["value"] = value
	}
	if err := b.emit(map[string]any{"id": id, "method": method, "args": args}); err != nil {
		return err
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case response := <-ch:
		if response.Error != "" {
			return errors.New("storage")
		}
		if target != nil && json.Unmarshal(response.Result, target) != nil {
			return errors.New("storage")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return errors.New("ipc")
	case <-timer.C:
		return errors.New("storage")
	}
}

func category(err error) string {
	switch err.Error() {
	case "not_connected", "invalid_destination", "account_changed", "route_changed", "permission", "storage", "invalid_request":
		return err.Error()
	default:
		return "network"
	}
}

// All stdout is framed private IPC. No library, account, QR or error diagnostics
// are written to files/stderr. The parent alone owns OS-protected key storage.
func run(input io.Reader, output io.Writer) int {
	b := newBridge(output)
	a := &account{bridge: b}
	commands := make(chan packet, 32)
	done := make(chan struct{})
	defer func() { b.cancel(); <-done; a.close() }()
	go func() {
		defer close(done)
		defer func() {
			if recover() != nil {
				b.cancel()
			}
		}()
		for {
			select {
			case <-b.ctx.Done():
				// EOF cancels storage/network waits. A queued offline close still
				// receives its acknowledgement; no other queued command can send.
				for len(commands) > 0 {
					request := <-commands
					if request.Method == "close" {
						a.close()
						b.reply(request.ID, nil, nil)
					}
				}
				return
			case request := <-commands:
				ctx, cancel := context.WithTimeout(b.ctx, 80*time.Second)
				result, err := a.command(ctx, request.Method, request.Args)
				cancel()
				b.reply(request.ID, result, err)
				if request.Method == "close" {
					b.cancel()
					return
				}
			}
		}
	}()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 4_000_000)
	for scanner.Scan() {
		var request packet
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.ID == "" {
			return 2
		}
		if request.Method == "" {
			if ch, ok := b.waiters.Load(request.ID); ok {
				select {
				case ch.(chan packet) <- request:
				default:
				}
			}
		} else {
			select {
			case commands <- request:
			case <-b.ctx.Done():
				return 0
			default:
				b.reply(request.ID, nil, errors.New("busy"))
			}
		}
		if b.ctx.Err() != nil {
			return 0
		}
	}
	b.cancel()
	if scanner.Err() != nil {
		return 2
	}
	return 0
}
func main() {
	defer func() {
		if recover() != nil {
			os.Exit(2)
		}
	}()
	os.Exit(run(os.Stdin, os.Stdout))
}
