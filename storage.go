// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/xts"
	"go.mau.fi/whatsmeow/store/sqlstore"
)

func openDatabase(ctx context.Context, root string, key []byte) (*sql.DB, error) {
	if !filepath.IsAbs(root) || len(key) != 64 {
		return nil, errors.New("storage")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("storage")
	}
	path := filepath.ToSlash(filepath.Join(root, "account.db"))
	uriPath := (&url.URL{Path: path}).EscapedPath()
	// Key is applied to every pooled connection, never included in URI/arguments.
	db, err := driver.Open("file:"+uriPath+"?vfs=xts&_txlock=immediate", func(conn *sqlite3.Conn) error {
		if err := conn.Exec("PRAGMA hexkey='" + hex.EncodeToString(key) + "'; PRAGMA busy_timeout=10000; PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA temp_store=MEMORY;"); err != nil {
			return errors.New("storage")
		}
		return nil
	})
	if err != nil {
		return nil, errors.New("storage")
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	container := sqlstore.NewWithDB(db, "sqlite3", nil)
	if err = container.Upgrade(ctx); err == nil {
		_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS luqta_sends (id TEXT PRIMARY KEY, account TEXT NOT NULL, destination TEXT NOT NULL, message BLOB NOT NULL, confirmed BOOLEAN NOT NULL DEFAULT FALSE)`)
	}
	if err != nil {
		db.Close()
		return nil, errors.New("storage")
	}
	return db, nil
}

func (a *account) databaseKey(ctx context.Context, root string) ([]byte, error) {
	var key []byte
	if err := a.bridge.storage(ctx, "store.get", "wa/whatsmeow/database-key", nil, &key); err != nil {
		return nil, err
	}
	if len(key) == 0 {
		// A missing protection key must never silently reset an existing session DB.
		if _, err := os.Stat(filepath.Join(root, "account.db")); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("storage")
		}
		key = make([]byte, 64)
		if _, err := rand.Read(key); err != nil {
			return nil, errors.New("storage")
		}
		if err := a.bridge.storage(ctx, "store.put", "wa/whatsmeow/database-key", key, nil); err != nil {
			clear(key)
			return nil, err
		}
	}
	if len(key) != 64 {
		return nil, errors.New("storage")
	}
	return key, nil
}
