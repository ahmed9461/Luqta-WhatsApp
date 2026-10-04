// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

type account struct {
	bridge *bridge
	db     *sql.DB
	client *whatsmeow.Client
	key    []byte
	mu     sync.Mutex
}
type destination struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Group bool   `json:"group"`
}

func (a *account) command(ctx context.Context, method string, args json.RawMessage) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch method {
	case "connect":
		var input struct {
			Root string `json:"root"`
		}
		if json.Unmarshal(args, &input) != nil {
			return nil, errors.New("invalid_request")
		}
		return nil, a.connect(ctx, input.Root)
	case "disconnect":
		if a.client != nil {
			if err := a.client.Logout(ctx); err != nil {
				return nil, err
			}
			a.client.Disconnect()
			a.client = nil
		}
		a.bridge.state("disconnected", "", "")
		return nil, nil
	case "destinations":
		if err := a.ready(); err != nil {
			return nil, err
		}
		groups, err := a.client.GetJoinedGroups(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]destination, 0, len(groups))
		for _, group := range groups {
			if groupSendable(group, *a.client.Store.ID, a.client.Store.LID) {
				result = append(result, destination{group.JID.String(), group.Name, true})
			}
		}
		return result, nil
	case "resolve":
		if err := a.ready(); err != nil {
			return nil, err
		}
		var input struct {
			Input string `json:"input"`
		}
		if json.Unmarshal(args, &input) != nil {
			return nil, errors.New("invalid_request")
		}
		phone := strings.NewReplacer(" ", "", "(", "", ")", "", "+", "", "-", "", "\t", "", "\n", "").Replace(input.Input)
		if !phonePattern.MatchString(phone) {
			return nil, errors.New("invalid_destination")
		}
		users, err := a.client.IsOnWhatsApp(ctx, []string{phone})
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if user.IsIn {
				return destination{user.JID.ToNonAD().String(), "+" + phone, false}, nil
			}
		}
		return nil, errors.New("invalid_destination")
	case "deliver":
		var input delivery
		if json.Unmarshal(args, &input) != nil {
			return nil, errors.New("invalid_request")
		}
		return a.deliver(ctx, input)
	case "release":
		var input delivery
		if json.Unmarshal(args, &input) != nil || !idPattern.MatchString(input.ID) {
			return nil, errors.New("invalid_request")
		}
		if err := a.ensureDatabase(ctx, input.Root); err != nil {
			return nil, err
		}
		return nil, releaseSend(ctx, a.db, input)
	case "close":
		a.closeLocked()
		return nil, nil
	default:
		return nil, errors.New("invalid_request")
	}
}

var phonePattern = regexp.MustCompile(`^[1-9][0-9]{6,14}$`)

func (a *account) ready() error {
	if a.client == nil || a.client.Store.ID == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		return errors.New("not_connected")
	}
	return nil
}
func (a *account) connect(ctx context.Context, root string) error {
	if a.client != nil {
		if a.client.IsConnected() {
			if a.client.IsLoggedIn() {
				a.bridge.state("ready", a.client.Store.ID.ToNonAD().String(), "")
			}
			return nil
		}
		if a.client.Store.ID != nil && !a.client.Store.Deleted {
			a.bridge.state("connecting", "", "")
			return a.client.ConnectContext(a.bridge.ctx)
		}
		a.client.Disconnect()
		a.client = nil
	}
	if err := a.ensureDatabase(ctx, root); err != nil {
		return err
	}
	container := sqlstore.NewWithDB(a.db, "sqlite3", nil)
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return errors.New("storage")
	}
	client := whatsmeow.NewClient(device, waLog.Noop)
	client.ManualHistorySyncDownload = true
	client.UseRetryMessageStore = true
	client.AutomaticMessageRerequestFromPhone = false
	client.AddEventHandler(func(event any) {
		switch event.(type) {
		case *events.Connected:
			a.bridge.state("ready", client.Store.ID.ToNonAD().String(), "")
		case *events.Disconnected, events.PermanentDisconnect, *events.PairError:
			a.bridge.state("disconnected", "", "")
		}
	})
	a.client = client
	if device.ID == nil {
		qr, err := client.GetQRChannel(a.bridge.ctx)
		if err != nil {
			return err
		}
		go func() {
			for item := range qr {
				if item.Event == "code" {
					a.bridge.state("qr", "", item.Code)
				}
				if item.Event == "timeout" || item.Event == "error" {
					a.bridge.state("disconnected", "", "")
				}
			}
		}()
	}
	a.bridge.state("connecting", "", "")
	return client.ConnectContext(a.bridge.ctx)
}

func (a *account) ensureDatabase(ctx context.Context, root string) error {
	if a.db == nil {
		key, err := a.databaseKey(ctx, root)
		if err != nil {
			return err
		}
		db, err := openDatabase(ctx, root, key)
		if err != nil {
			clear(key)
			return err
		}
		a.db = db
		a.key = key
	}
	return nil
}
func groupSendable(group *types.GroupInfo, phone, lid types.JID) bool {
	if group.Suspended || group.IsParent {
		return false
	}
	phone = phone.ToNonAD()
	lid = lid.ToNonAD()
	for _, p := range group.Participants {
		for _, id := range []types.JID{p.JID, p.PhoneNumber, p.LID} {
			if !id.IsEmpty() && (id.ToNonAD() == phone || !lid.IsEmpty() && id.ToNonAD() == lid) {
				return !group.IsAnnounce || p.IsAdmin || p.IsSuperAdmin
			}
		}
	}
	return false
}
func (a *account) closeLocked() {
	if a.client != nil {
		a.client.Disconnect()
		a.client = nil
	}
	if a.db != nil {
		_ = a.db.Close()
		a.db = nil
	}
	clear(a.key)
	a.key = nil
}
func (a *account) close() { a.mu.Lock(); defer a.mu.Unlock(); a.closeLocked() }
