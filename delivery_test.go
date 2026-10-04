// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"errors"
	"testing"
)

func TestLostAckRetryUsesSameMessageAndConfirmsOnce(t *testing.T) {
	db, root, key := fixtureDB(t)
	ctx := context.Background()
	d := delivery{ID: "123", Account: "owner", Destination: "target", AllowNewSend: true}
	prepared, relayed := 0, 0
	prepare := func(context.Context) ([]byte, error) { prepared++; return []byte("stable-message"), nil }
	relay := func(_ context.Context, body []byte) error {
		relayed++
		if string(body) != "stable-message" {
			t.Fatal("message changed")
		}
		if relayed == 1 {
			return errors.New("lost ACK")
		}
		return nil
	}
	if state, err := deliverOnce(ctx, db, d, prepare, relay); err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	db.Close()
	db2, err := openDatabase(ctx, root, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if state, err := deliverOnce(ctx, db2, d, prepare, relay); err != nil || state != "confirmed" {
		t.Fatal(state, err)
	}
	if state, err := deliverOnce(ctx, db2, d, prepare, relay); err != nil || state != "confirmed" {
		t.Fatal(state, err)
	}
	if prepared != 1 || relayed != 2 {
		t.Fatal("duplicate preparation/confirmed relay")
	}
}
func TestPendingRouteCannotChange(t *testing.T) {
	db, _, _ := fixtureDB(t)
	ctx := context.Background()
	d := delivery{ID: "123", Account: "owner", Destination: "target", AllowNewSend: true}
	prepare := func(context.Context) ([]byte, error) { return []byte("message"), nil }
	relay := func(context.Context, []byte) error { return errors.New("offline") }
	_, _ = deliverOnce(ctx, db, d, prepare, relay)
	d.Destination = "other"
	if _, err := deliverOnce(ctx, db, d, prepare, relay); err == nil || err.Error() != "route_changed" {
		t.Fatal("route changed")
	}
	d.Destination = "target"
	d.Account = "other"
	if _, err := deliverOnce(ctx, db, d, prepare, relay); err == nil || err.Error() != "route_changed" {
		t.Fatal("account changed")
	}
}
func TestNoNewSendAfterSessionExpiry(t *testing.T) {
	db, _, _ := fixtureDB(t)
	state, err := deliverOnce(context.Background(), db, delivery{ID: "123", AllowNewSend: false}, func(context.Context) ([]byte, error) { t.Fatal("new message after expiry"); return nil, nil }, func(context.Context, []byte) error { t.Fatal("relay after expiry"); return nil })
	if err != nil || state != "uncertain" {
		t.Fatal(state, err)
	}
}
func TestPersistenceFailurePreventsRelay(t *testing.T) {
	db, _, _ := fixtureDB(t)
	db.Close()
	if _, err := deliverOnce(context.Background(), db, delivery{ID: "123", AllowNewSend: true}, func(context.Context) ([]byte, error) { return []byte("test"), nil }, func(context.Context, []byte) error { t.Fatal("relay without durable state"); return nil }); err == nil {
		t.Fatal("storage failure ignored")
	}
}

func TestFinalizedReceiptsReleaseIdempotentlyButPendingAndChangedRoutesRemain(t *testing.T) {
	db, _, _ := fixtureDB(t)
	ctx := context.Background()
	d := delivery{ID: "123", Account: "owner", Destination: "target", AllowNewSend: true}
	prepare := func(context.Context) ([]byte, error) { return []byte("message"), nil }
	_, _ = deliverOnce(ctx, db, d, prepare, func(context.Context, []byte) error { return errors.New("offline") })
	if releaseSend(ctx, db, d) == nil {
		t.Fatal("pending receipt removed")
	}
	_, _ = deliverOnce(ctx, db, d, prepare, func(context.Context, []byte) error { return nil })
	changed := d
	changed.Account = "other"
	if releaseSend(ctx, db, changed) == nil {
		t.Fatal("wrong account cleanup accepted")
	}
	if releaseSend(ctx, db, d) != nil || releaseSend(ctx, db, d) != nil {
		t.Fatal("finalization not idempotent")
	}
	if record, err := loadSend(ctx, db, d.ID); err != nil || record != nil {
		t.Fatal("confirmed receipt retained")
	}
}
