// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOfflineCloseWithInputEOFStillAcknowledges(t *testing.T) {
	for range 30 {
		var out bytes.Buffer
		if run(strings.NewReader("{\"id\":\"offline\",\"method\":\"close\",\"args\":{}}\n"), &out) != 0 {
			t.Fatal("close failed")
		}
		var result packet
		if json.Unmarshal(out.Bytes(), &result) != nil || result.ID != "offline" || string(result.Result) != "null" || result.Error != "" {
			t.Fatal("missing close acknowledgement")
		}
	}
}
func TestMalformedIPCStopsWithoutEchoingPayload(t *testing.T) {
	var out bytes.Buffer
	if run(strings.NewReader("invalid-sensitive-payload\n"), &out) != 2 || out.Len() != 0 {
		t.Fatal("malformed IPC was accepted or echoed")
	}
}
