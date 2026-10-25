// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"go.mau.fi/whatsmeow/types/events"
)

type captureScope struct {
	Scope       string `json:"scope"`
	Session     string `json:"session"`
	Account     string `json:"account"`
	Destination string `json:"destination"`
	Command     string `json:"command"`
}
type captureCommand struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

func normalizeCommand(text string) string { return strings.Join(strings.Fields(text), " ") }

// No conversation text is retained. Only an exact match in the configured chat
// reaches SQLite. FromMe is deliberately accepted, including linked-device echoes.
func receiveCaptureCommand(ctx context.Context, db *sql.DB, scope *captureScope, account string, event *events.Message, alternate string) (bool, error) {
	if scope == nil || scope.Account != account || event.IsEdit || event.Info.ID == "" || event.Message == nil {
		return false, nil
	}
	chat := event.Info.Chat.ToNonAD().String()
	if scope.Destination != chat && scope.Destination != alternate {
		return false, nil
	}
	text := event.Message.GetConversation()
	if extended := event.Message.GetExtendedTextMessage(); extended != nil {
		text = extended.GetText()
	}
	if text == "" || normalizeCommand(text) != scope.Command {
		return false, nil
	}
	// Canonical selected chat makes PN/LID event aliases share one durable key.
	hash := sha256.Sum256([]byte(account + "\x00" + scope.Destination + "\x00" + event.Info.ID))
	id := hex.EncodeToString(hash[:])
	result, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO luqta_capture_commands(id,scope) VALUES(?,?)`, id, scope.Scope)
	if err != nil {
		return false, errors.New("storage")
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (a *account) captureCommand(ctx context.Context, method string, args json.RawMessage) (any, error) {
	if method == "capture.configure" {
		var input struct {
			Scope *captureScope `json:"scope"`
		}
		if json.Unmarshal(args, &input) != nil {
			return nil, errors.New("invalid_request")
		}
		if input.Scope != nil {
			if err := a.ready(); err != nil {
				return nil, err
			}
			scope := input.Scope
			if len(scope.Scope) != 64 || scope.Session == "" || scope.Destination == "" || normalizeCommand(scope.Command) == "" {
				return nil, errors.New("invalid_request")
			}
			if scope.Account != a.client.Store.ID.ToNonAD().String() {
				return nil, errors.New("account_changed")
			}
			scope.Command = normalizeCommand(scope.Command)
		}
		a.captureScope.Store(input.Scope)
		return nil, nil
	}
	if a.db == nil {
		return nil, errors.New("not_connected")
	}
	if method == "capture.ack" {
		var input struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(args, &input) != nil {
			return nil, errors.New("invalid_request")
		}
		_, err := a.db.ExecContext(ctx, `UPDATE luqta_capture_commands SET acknowledged=TRUE WHERE id=?`, input.ID)
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id,scope FROM luqta_capture_commands WHERE acknowledged=FALSE ORDER BY sequence LIMIT 16`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]captureCommand, 0)
	for rows.Next() {
		var command captureCommand
		if err := rows.Scan(&command.ID, &command.Scope); err != nil {
			return nil, err
		}
		result = append(result, command)
	}
	return result, rows.Err()
}
