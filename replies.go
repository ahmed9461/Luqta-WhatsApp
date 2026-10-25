// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

type incomingReply struct {
	ID         string    `json:"id"`
	Session    string    `json:"session"`
	Screenshot string    `json:"screenshot"`
	Text       string    `json:"text"`
	ReceivedAt time.Time `json:"receivedAt"`
}

func replyBody(message *waE2E.Message) (string, *waE2E.ContextInfo) {
	if message == nil {
		return "", nil
	}
	if extended := message.GetExtendedTextMessage(); extended != nil {
		return extended.GetText(), extended.GetContextInfo()
	}
	return "", nil // Plain conversation/media/reactions are not text replies.
}

func displayText(text string) bool {
	for _, r := range text {
		if !unicode.IsSpace(r) && !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) {
			return true
		}
	}
	return false
}

// Called from whatsmeow's message event. Quoted content is never trusted for
// routing: the stanza ID, current account and actual chat must match our ledger.
func receiveReply(ctx context.Context, db *sql.DB, account string, event *events.Message) error {
	return receiveReplyWithAlternate(ctx, db, account, event, "")
}

func receiveReplyWithAlternate(ctx context.Context, db *sql.DB, account string, event *events.Message, alternateChat string) error {
	text, quoted := replyBody(event.Message)
	if event.IsEdit || quoted == nil || quoted.GetStanzaID() == "" || !displayText(text) || event.Info.ID == "" {
		return nil
	}
	var session string
	err := db.QueryRowContext(ctx, `SELECT session FROM luqta_screenshot_routes
        WHERE id=? AND account=? AND destination IN (?,?)`, quoted.GetStanzaID(), account, event.Info.Chat.ToNonAD().String(), alternateChat).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("storage")
	}
	if session == "" {
		return nil
	}
	// Include sender in identity; group senders can reuse message IDs. Account
	// and chat scope protect separate linked accounts and destinations.
	id := fmt.Sprintf("wa:%s:%s:%s:%s", account, event.Info.Chat.ToNonAD(), event.Info.Sender.ToNonAD(), event.Info.ID)
	body, err := json.Marshal(incomingReply{ID: id, Session: session, Screenshot: quoted.GetStanzaID(), Text: text, ReceivedAt: time.Now().UTC()})
	if err != nil {
		return errors.New("storage")
	}
	_, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO luqta_replies(id,session,payload) VALUES(?,?,?)`, id, session, body)
	if err != nil {
		return errors.New("storage")
	}
	return nil
}

func readReplies(ctx context.Context, db *sql.DB, session string) ([]incomingReply, error) {
	rows, err := db.QueryContext(ctx, `SELECT payload FROM luqta_replies WHERE session=? AND acknowledged=FALSE ORDER BY sequence LIMIT 16`, session)
	if err != nil {
		return nil, errors.New("storage")
	}
	defer rows.Close()
	result := make([]incomingReply, 0)
	for rows.Next() {
		var body []byte
		var reply incomingReply
		if rows.Scan(&body) != nil || json.Unmarshal(body, &reply) != nil {
			return nil, errors.New("storage")
		}
		result = append(result, reply)
	}
	return result, rows.Err()
}

func replyCommand(ctx context.Context, db *sql.DB, method string, args json.RawMessage) (any, error) {
	var input struct {
		Session string `json:"session"`
		ID      string `json:"id"`
	}
	if json.Unmarshal(args, &input) != nil || strings.TrimSpace(input.Session) == "" {
		return nil, errors.New("invalid_request")
	}
	if method == "replies" {
		return readReplies(ctx, db, input.Session)
	}
	_, err := db.ExecContext(ctx, `UPDATE luqta_replies SET acknowledged=TRUE,payload=NULL WHERE id=? AND session=?`, input.ID, input.Session)
	if err != nil {
		return nil, errors.New("storage")
	}
	return nil, nil
}
