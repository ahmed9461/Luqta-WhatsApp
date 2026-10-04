// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestPackagedHelperOfflineLifecycleAndProtectedStorageFailure(t *testing.T) {
	helper := os.Getenv("LUQTA_WHATSAPP_HELPER")
	if helper == "" {
		t.Skip("packaged helper path not provided")
	}
	for _, scenario := range []string{"close", "storage-failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, helper)
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				in.Close()
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			scan := bufio.NewScanner(out)
			send := json.NewEncoder(in)
			read := func() packet {
				t.Helper()
				if !scan.Scan() {
					t.Fatal("no IPC reply")
				}
				var p packet
				if json.Unmarshal(scan.Bytes(), &p) != nil {
					t.Fatal("invalid IPC reply")
				}
				return p
			}
			if scenario == "storage-failure" {
				_ = send.Encode(map[string]any{"id": "connect", "method": "connect", "args": map[string]string{"root": t.TempDir()}})
				request := read()
				if request.Method != "store.get" {
					t.Fatal("protection broker bypassed")
				}
				_ = send.Encode(map[string]any{"id": request.ID, "error": "storage"})
				response := read()
				if response.ID != "connect" || response.Error != "storage" {
					t.Fatal("storage failure not preserved")
				}
			}
			_ = send.Encode(map[string]any{"id": "close", "method": "close", "args": map[string]any{}})
			response := read()
			if response.ID != "close" || string(response.Result) != "null" {
				t.Fatal("close not acknowledged")
			}
			_ = in.Close()
			if cmd.Wait() != nil || diagnostics.Len() != 0 {
				t.Fatal("unclean exit or diagnostics exposed")
			}
		})
	}
}
