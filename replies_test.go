// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func replyEvent(id, chat, quoted, text string) *events.Message {
	jid, _ := types.ParseJID(chat)
	return &events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: jid, Sender: types.NewJID("sender", types.DefaultUserServer)}, ID: id},
		Message: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String(quoted)}}},
	}
}

func TestReplyUsesImmutableScreenshotChatAccountAndSession(t *testing.T) {
	db, root, key := fixtureDB(t)
	ctx := context.Background()
	d := delivery{ID: "ABC", Account: "account", Destination: "target@s.whatsapp.net", Session: "session-A", AllowNewSend: true}
	_, err := deliverOnce(ctx, db, d, func(context.Context) ([]byte, error) { return []byte("image"), nil }, func(context.Context, []byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = releaseSend(ctx, db, d); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ account, chat, quote, text string }{
		{"other", "target@s.whatsapp.net", "ABC", "wrong account"},
		{"account", "other@s.whatsapp.net", "ABC", "wrong chat"},
		{"account", "target@s.whatsapp.net", "OTHER", "other screenshot"},
		{"account", "target@s.whatsapp.net", "", "normal message"},
		{"account", "target@s.whatsapp.net", "ABC", " \n\u200d"},
	}
	for _, test := range tests {
		if err = receiveReply(ctx, db, test.account, replyEvent("1", test.chat, test.quote, test.text)); err != nil {
			t.Fatal(err)
		}
	}
	replies, err := readReplies(ctx, db, "session-A")
	if err != nil || len(replies) != 0 {
		t.Fatal("unexpected reply", err)
	}
	texts := []string{"a", "-", "123", "👩🏽‍💻👨‍👩‍👧‍👦🇸🇦", "مرحبا", "English", "عربي English", "one\nاثنان", strings.Repeat("ع", 50000) + "👩🏽‍💻"}
	for i, text := range texts {
		event := replyEvent(string(rune('A'+i)), d.Destination, d.ID, text)
		if err = receiveReply(ctx, db, d.Account, event); err != nil {
			t.Fatal(err)
		}
		if err = receiveReply(ctx, db, d.Account, event); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	reopened, err := openDatabase(ctx, root, key)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replies, err = readReplies(ctx, reopened, "session-A")
	if err != nil || len(replies) != len(texts) {
		t.Fatal("lost/duplicate reply", len(replies), err)
	}
	for i, reply := range replies {
		if reply.Text != texts[i] {
			t.Fatal("text changed")
		}
	}
	other, err := readReplies(ctx, reopened, "session-B")
	if err != nil || len(other) != 0 {
		t.Fatal("cross-session reply")
	}
	for _, reply := range replies {
		args, _ := json.Marshal(map[string]string{"id": reply.ID, "session": "session-A"})
		if _, err = replyCommand(ctx, reopened, "reply.ack", args); err != nil {
			t.Fatal(err)
		}
		// A reconnect must not re-enqueue already acknowledged provider events.
	}
	if err = receiveReply(ctx, reopened, d.Account, replyEvent("A", d.Destination, d.ID, texts[0])); err != nil {
		t.Fatal(err)
	}
	replies, err = readReplies(ctx, reopened, "session-A")
	if err != nil || len(replies) != 0 {
		t.Fatal("ack replay duplicated")
	}
}

func TestScreenshotRouteCannotMoveToAnotherSession(t *testing.T) {
	db, _, _ := fixtureDB(t)
	ctx := context.Background()
	d := delivery{ID: "ABC", Account: "account", Destination: "target", Session: "first", AllowNewSend: true}
	prepare := func(context.Context) ([]byte, error) { return []byte("image"), nil }
	relay := func(context.Context, []byte) error { return context.Canceled }
	_, _ = deliverOnce(ctx, db, d, prepare, relay)
	d.Session = "other"
	if _, err := deliverOnce(ctx, db, d, prepare, relay); err == nil || err.Error() != "route_changed" {
		t.Fatal("session route changed")
	}
}

func TestAuthenticatedPNLIDAliasesAndPersistenceFailure(t *testing.T) {
    db,_,_:=fixtureDB(t);ctx:=context.Background()
    _,err:=db.ExecContext(ctx, `INSERT INTO luqta_screenshot_routes(id,account,destination,session) VALUES('ABC','account','target@lid','session')`)
    if err!=nil{t.Fatal(err)}
    event:=replyEvent("1","target@s.whatsapp.net","ABC","-")
    if err=receiveReplyWithAlternate(ctx,db,"account",event,"target@lid");err!=nil{t.Fatal(err)}
    if err=receiveReplyWithAlternate(ctx,db,"account",event,"target@lid");err!=nil{t.Fatal(err)}
    replies,err:=readReplies(ctx,db,"session");if err!=nil||len(replies)!=1{t.Fatal("alias routing/duplicate",err)}
    event.IsEdit=true
    event.Info.ID="2"
    if err=receiveReplyWithAlternate(ctx,db,"account",event,"target@lid");err!=nil{t.Fatal(err)}
    replies,_=readReplies(ctx,db,"session");if len(replies)!=1{t.Fatal("edit displayed as new instruction")}
    event.IsEdit=false
    db.Close()
    if err=receiveReplyWithAlternate(ctx,db,"account",event,"target@lid");err==nil{t.Fatal("failed persistence was acknowledged")}
}
