// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCaptureCommandExactTextSelectedChatBothSendersAndDurableDedup(t *testing.T) {
	for _, chat := range []string{"target@s.whatsapp.net", "target@g.us"} {
		for _, fromMe := range []bool{false, true} {
			t.Run(chat+string(rune('0'+btoi(fromMe))), func(t *testing.T) {
				db, root, key := fixtureDB(t)
				ctx := context.Background()
				scope := &captureScope{Scope: strings.Repeat("a", 64), Account: "account", Destination: chat, Session: "session", Command: "تصوير"}
				event := replyEvent("message", chat, "", "  تصوير \t\n")
				event.Info.IsFromMe = fromMe
				added, err := receiveCaptureCommand(ctx, db, scope, "account", event, "")
				if err != nil || !added {
					t.Fatal("matching text rejected", err)
				}
				added, err = receiveCaptureCommand(ctx, db, scope, "account", event, "")
				if err != nil || added {
					t.Fatal("event repeated", err)
				}
				db.Close()
				reopened, err := openDatabase(ctx, root, key)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				scope.Scope = strings.Repeat("b", 64)
				added, err = receiveCaptureCommand(ctx, reopened, scope, "account", event, "")
				if err != nil || added {
					t.Fatal("reconnect/reconfigure duplicated event", err)
				}
			})
		}
	}
}
func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestCaptureCommandConcurrentReplayReservesOneIdentity(t *testing.T) {
	db, _, _ := fixtureDB(t)
	defer db.Close()
	scope := &captureScope{Scope: strings.Repeat("a", 64), Account: "account", Destination: "target@g.us", Command: "تصوير"}
	event := replyEvent("same-id", scope.Destination, "", "تصوير")
	var group sync.WaitGroup
	var added atomic.Int32
	errors := make(chan error, 32)
	for range 32 {
		group.Go(func() {
			ok, err := receiveCaptureCommand(context.Background(), db, scope, "account", event, "")
			if err != nil {
				errors <- err
			}
			if ok {
				added.Add(1)
			}
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	if added.Load() != 1 {
		t.Fatal("concurrent command duplicated")
	}
}

func TestCaptureCommandRejectsOrdinaryTextOtherChatsEditsMediaAndDisabled(t *testing.T) {
	db, _, _ := fixtureDB(t)
	defer db.Close()
	ctx := context.Background()
	scope := &captureScope{Scope: strings.Repeat("a", 64), Account: "account", Destination: "target@g.us", Command: "تصوير"}
	for _, text := range []string{"ممكن تصوير الشاشة؟", "تصوير الآن", "", " \t"} {
		added, err := receiveCaptureCommand(ctx, db, scope, "account", replyEvent(text, scope.Destination, "", text), "")
		if err != nil || added {
			t.Fatal("ordinary text triggered capture", err)
		}
	}
	event := replyEvent("1", scope.Destination, "", "تصوير")
	if added, err := receiveCaptureCommand(ctx, db, nil, "account", event, ""); err != nil || added {
		t.Fatal("disabled", err)
	}
	if added, err := receiveCaptureCommand(ctx, db, scope, "other", event, ""); err != nil || added {
		t.Fatal("foreign account", err)
	}
	event.Info.Chat.User = "other"
	if added, err := receiveCaptureCommand(ctx, db, scope, "account", event, ""); err != nil || added {
		t.Fatal("foreign chat", err)
	}
	event = replyEvent("2", scope.Destination, "", "تصوير")
	event.IsEdit = true
	if added, err := receiveCaptureCommand(ctx, db, scope, "account", event, ""); err != nil || added {
		t.Fatal("edit", err)
	}
	event.IsEdit = false
	event.Message = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("تصوير")}}
	if added, err := receiveCaptureCommand(ctx, db, scope, "account", event, ""); err != nil || added {
		t.Fatal("media caption", err)
	}
	scope.Command = normalizeCommand("  ممكن تصوير الشاشة؟  ")
	event = replyEvent("3", scope.Destination, "", "ممكن تصوير الشاشة؟")
	if added, err := receiveCaptureCommand(ctx, db, scope, "account", event, ""); err != nil || !added {
		t.Fatal("custom full phrase", err)
	}
	scope.Command = "تصوير"
	event = replyEvent("4", "alias@lid", "", "تصوير")
	if added, err := receiveCaptureCommand(ctx, db, scope, "account", event, scope.Destination); err != nil || !added {
		t.Fatal("authenticated alternate chat", err)
	}
}
