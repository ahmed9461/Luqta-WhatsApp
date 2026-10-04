// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
)

func fixtureDB(t *testing.T) (*sql.DB, string, []byte) {
	t.Helper()
	root := t.TempDir()
	key := make([]byte, 64)
	_, _ = rand.Read(key)
	db, err := openDatabase(context.Background(), root, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); clear(key) })
	return db, root, key
}
func TestEncryptedSessionRestartAndWrongKey(t *testing.T) {
	db, root, key := fixtureDB(t)
	ctx := context.Background()
	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jid := types.NewADJID("19990000000", 0, 1)
	device.ID = &jid
	device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte("fixture"), AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
	device.PushName = "SYNTHETIC-SESSION-PLAINTEXT-MARKER"
	if err = device.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	bytesOnDisk, err := os.ReadFile(filepath.Join(root, "account.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytesOnDisk, []byte("SQLite format 3")) || bytes.Contains(bytesOnDisk, []byte(device.PushName)) || bytes.Contains(bytesOnDisk, device.NoiseKey.Priv[:]) {
		t.Fatal("session stored unencrypted")
	}
	db2, err := openDatabase(ctx, root, key)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	restored, err := sqlstore.NewWithDB(db2, "sqlite3", nil).GetFirstDevice(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID == nil || *restored.ID != jid || restored.PushName != device.PushName {
		t.Fatal("session not retained")
	}
	wrong := append([]byte(nil), key...)
	wrong[0] ^= 1
	if bad, err := openDatabase(ctx, root, wrong); err == nil {
		bad.Close()
		t.Fatal("wrong key accepted")
	}
}
func TestEveryConnectionEncryptedAndForeignKeys(t *testing.T) {
	db, root, _ := fixtureDB(t)
	ctx := context.Background()
	connections := make([]*sql.Conn, 4)
	for i := range connections {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = c
		defer c.Close()
		var enabled bool
		if err = c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || !enabled {
			t.Fatal("connection not initialized")
		}
		if _, err = c.ExecContext(ctx, "INSERT INTO luqta_sends(id,account,destination,message) VALUES(?,?,?,?)", string(rune('A'+i)), "owner", "target", []byte("ENCRYPTED-WAL-MARKER")); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "account.db*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("ENCRYPTED-WAL-MARKER")) {
			t.Fatal("plaintext WAL")
		}
	}
}
func TestInvalidKeyNeverCreatesState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	if db, err := openDatabase(context.Background(), root, make([]byte, 32)); err == nil {
		db.Close()
		t.Fatal("invalid key accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid configuration wrote state")
	}
}
