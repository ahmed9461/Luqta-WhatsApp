// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"regexp"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type delivery struct {
	Root         string `json:"root"`
	ID           string `json:"id"`
	Account      string `json:"account"`
	Destination  string `json:"destination"`
	File         string `json:"file"`
	AllowNewSend bool   `json:"allowNewSend"`
	Session      string `json:"session"`
}
type sendRecord struct {
	Account, Destination string
	Message              []byte
	Confirmed            bool
}

var idPattern = regexp.MustCompile(`^[A-F0-9]{32}$`)

// Numeric-only timing travels over private IPC. No identifiers or payloads.
type sendTiming struct {
	TotalMs, PermissionMs, UploadMs, RelayMs         float64
	LidMs, DeviceMs, ParticipantsMs, InternalQueueMs float64
	EncryptMs, ResponseMs, RetryMs                   float64
	Bytes                                            int64
}
type deliveryResult struct {
	Status string     `json:"status"`
	Timing sendTiming `json:"timing"`
}

// The host calls this only after its durable confirmed->finalizing transition
// and image/intent deletion. Missing is idempotent after a lost cleanup reply.
func releaseSend(ctx context.Context, db *sql.DB, d delivery) error {
	r, err := loadSend(ctx, db, d.ID)
	if err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	if !r.Confirmed || r.Account != d.Account || r.Destination != d.Destination {
		return errors.New("receipt_pending")
	}
	if _, err = db.ExecContext(ctx, "DELETE FROM luqta_sends WHERE id=? AND account=? AND destination=? AND confirmed=TRUE", d.ID, d.Account, d.Destination); err != nil {
		return errors.New("storage")
	}
	return nil
}

func loadSend(ctx context.Context, db *sql.DB, id string) (*sendRecord, error) {
	r := new(sendRecord)
	err := db.QueryRowContext(ctx, "SELECT account,destination,message,confirmed FROM luqta_sends WHERE id=?", id).Scan(&r.Account, &r.Destination, &r.Message, &r.Confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("storage")
	}
	return r, nil
}
func (a *account) deliver(ctx context.Context, d delivery) (deliveryResult, error) {
	started := time.Now()
	timing := sendTiming{}
	status, err := a.deliverCore(ctx, d, &timing)
	timing.TotalMs = float64(time.Since(started)) / float64(time.Millisecond)
	return deliveryResult{Status: status, Timing: timing}, err
}
func (a *account) deliverCore(ctx context.Context, d delivery, timing *sendTiming) (string, error) {
	if !idPattern.MatchString(d.ID) {
		return "", errors.New("invalid_request")
	}
	if err := a.ready(); err != nil {
		return "", err
	}
	if a.client.Store.ID.ToNonAD().String() != d.Account {
		return "", errors.New("account_changed")
	}
	to, err := types.ParseJID(d.Destination)
	if err != nil || to.Device != 0 || to.User == "" || (to.Server != types.DefaultUserServer && to.Server != types.HiddenUserServer && to.Server != types.GroupServer) {
		return "", errors.New("invalid_destination")
	}
	if to.Server == types.GroupServer {
		started := time.Now()
		group, err := a.client.GetGroupInfo(ctx, to)
		timing.PermissionMs = float64(time.Since(started)) / float64(time.Millisecond)
		if err != nil {
			return "", err
		}
		if !groupSendable(group, *a.client.Store.ID, a.client.Store.LID) {
			return "", errors.New("permission")
		}
	}
	return deliverOnce(ctx, a.db, d, func(ctx context.Context) ([]byte, error) {
		// Old transport receipts cannot be interpreted as a new send. Preserve
		// ambiguous legacy work rather than generate a second message on migration.
		var legacy json.RawMessage
		if err := a.bridge.storage(ctx, "store.get", "wa/send/"+d.ID, nil, &legacy); err != nil {
			return nil, err
		}
		if len(legacy) > 0 && string(legacy) != "null" {
			return nil, errors.New("legacy_uncertain")
		}
		file, err := os.Open(d.File)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.Size() > 32*1024*1024 || info.Size() < 8 {
			return nil, errors.New("image")
		}
		image, err := os.ReadFile(d.File)
		if err != nil {
			return nil, err
		}
		defer clear(image)
		size, err := png.DecodeConfig(bytes.NewReader(image))
		if err != nil || size.Width < 1 || size.Height < 1 {
			return nil, errors.New("image")
		}
		timing.Bytes = int64(len(image))
		started := time.Now()
		upload, err := a.client.Upload(ctx, image, whatsmeow.MediaImage)
		timing.UploadMs = float64(time.Since(started)) / float64(time.Millisecond)
		if err != nil {
			return nil, err
		}
		message := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{URL: &upload.URL, DirectPath: &upload.DirectPath, MediaKey: upload.MediaKey, FileEncSHA256: upload.FileEncSHA256, FileSHA256: upload.FileSHA256, FileLength: &upload.FileLength, Mimetype: proto.String("image/png"), Width: proto.Uint32(uint32(size.Width)), Height: proto.Uint32(uint32(size.Height))}}
		return proto.Marshal(message)
	}, func(ctx context.Context, body []byte) error {
		message := new(waE2E.Message)
		if proto.Unmarshal(body, message) != nil {
			return errors.New("storage")
		}
		started := time.Now()
		response, err := a.client.SendMessage(ctx, to, message, whatsmeow.SendRequestExtra{ID: d.ID})
		timing.RelayMs = float64(time.Since(started)) / float64(time.Millisecond)
		debug := response.DebugTimings
		timing.LidMs = float64(debug.LIDFetch) / float64(time.Millisecond)
		timing.DeviceMs = float64(debug.GetDevices) / float64(time.Millisecond)
		timing.ParticipantsMs = float64(debug.GetParticipants) / float64(time.Millisecond)
		timing.InternalQueueMs = float64(debug.Queue) / float64(time.Millisecond)
		timing.EncryptMs = float64(debug.PeerEncrypt+debug.GroupEncrypt) / float64(time.Millisecond)
		timing.ResponseMs = float64(debug.Resp) / float64(time.Millisecond)
		timing.RetryMs = float64(debug.Retry) / float64(time.Millisecond)
		return err
	})
}

// Called under account command serialization. Provider ACK is persisted before
// parent confirmation. Every retry uses identical content and message ID.
func deliverOnce(ctx context.Context, db *sql.DB, d delivery, prepare func(context.Context) ([]byte, error), relay func(context.Context, []byte) error) (string, error) {
	r, err := loadSend(ctx, db, d.ID)
	if err != nil {
		return "", err
	}
	if r != nil {
		if r.Account != d.Account || r.Destination != d.Destination {
			return "", errors.New("route_changed")
		}
		if r.Confirmed {
			return "confirmed", nil
		}
	} else {
		if !d.AllowNewSend {
			return "uncertain", nil
		}
		body, err := prepare(ctx)
		if err != nil {
			if err.Error() == "legacy_uncertain" {
				return "uncertain", nil
			}
			return "", err
		}
		if _, err = db.ExecContext(ctx, "INSERT INTO luqta_sends(id,account,destination,message) VALUES(?,?,?,?)", d.ID, d.Account, d.Destination, body); err != nil {
			return "", errors.New("storage")
		}
		r = &sendRecord{Account: d.Account, Destination: d.Destination, Message: body}
	}
	// Preserve a compact immutable screenshot route even after releasing the
	// encrypted image receipt. Reserve before relay so immediate replies match.
	if d.Session != "" {
		if _, err = db.ExecContext(ctx, `INSERT OR IGNORE INTO luqta_screenshot_routes(id,account,destination,session) VALUES(?,?,?,?)`, d.ID, d.Account, d.Destination, d.Session); err != nil {
			return "", errors.New("storage")
		}
		var account, destination, session string
		if db.QueryRowContext(ctx, `SELECT account,destination,session FROM luqta_screenshot_routes WHERE id=?`, d.ID).Scan(&account, &destination, &session) != nil || account != d.Account || destination != d.Destination || session != d.Session {
			return "", errors.New("route_changed")
		}
	}
	if err = relay(ctx, r.Message); err != nil {
		return "pending", nil
	}
	if _, err = db.ExecContext(ctx, "UPDATE luqta_sends SET confirmed=TRUE WHERE id=? AND account=? AND destination=?", d.ID, d.Account, d.Destination); err != nil {
		return "", errors.New("storage")
	}
	return "confirmed", nil
}
