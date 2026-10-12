package main

// WhatsApp increasingly addresses contacts by LID, an opaque user ID on the
// "lid" server, instead of by phone number. Stored as-is, one contact ends up
// split between an unnamed LID chat and their phone-number chat, and group
// senders show up as bare LIDs. The session keeps a LID to phone-number map,
// so chats and senders are stored by phone number whenever it is known.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// pnJID returns the phone-number JID for a LID the session can map, and any
// other JID unchanged.
func pnJID(client *whatsmeow.Client, jid types.JID) types.JID {
	if jid.Server != types.HiddenUserServer {
		return jid
	}
	pn, err := client.Store.LIDs.GetPNForLID(context.Background(), jid.ToNonAD())
	if err != nil || pn.IsEmpty() {
		return jid
	}
	return pn.ToNonAD()
}

// mediaDir is where downloadMedia keeps a chat's media.
func mediaDir(chatJID string) string {
	return fmt.Sprintf("store/%s", strings.ReplaceAll(chatJID, ":", "_"))
}

// mergeLIDChats moves every LID chat that toPN can map into the contact's
// phone-number chat (messages, cached transcriptions and downloaded media) and
// rewrites LID senders to phone numbers. It is idempotent and runs at startup,
// so mappings learned while running are applied on the next start.
func mergeLIDChats(store *MessageStore, toPN func(types.JID) types.JID, chatName func(types.JID) string, logger waLog.Logger) error {
	type lidChat struct {
		jid  types.JID
		last sql.NullTime
	}
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats WHERE jid LIKE '%@lid'")
	if err != nil {
		return err
	}
	var chats []lidChat
	for rows.Next() {
		var jid string
		var last sql.NullTime
		if err := rows.Scan(&jid, &last); err != nil {
			rows.Close()
			return err
		}
		if parsed, err := types.ParseJID(jid); err == nil {
			chats = append(chats, lidChat{parsed, last})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	withTranscriptions := tableExists(store.db, "transcriptions")
	merged := 0
	for _, chat := range chats {
		pn := toPN(chat.jid)
		if pn == chat.jid {
			continue
		}
		if err := mergeChat(store, chat.jid.String(), pn.String(), chat.last, chatName(pn), withTranscriptions); err != nil {
			return fmt.Errorf("merging %s into %s: %v", chat.jid, pn, err)
		}
		if err := moveMedia(chat.jid.String(), pn.String()); err != nil {
			logger.Warnf("Failed to move media of %s to %s: %v", chat.jid, pn, err)
		}
		merged++
	}

	senders, err := mapLIDSenders(store, toPN)
	if err != nil {
		return err
	}
	if merged > 0 || senders > 0 {
		logger.Infof("Merged %d LID chats into phone-number chats and mapped %d LID senders", merged, senders)
	}
	return nil
}

func mergeChat(store *MessageStore, lidChat, pnChat string, last sql.NullTime, name string, withTranscriptions bool) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Keep the latest activity of the two chats
	var pnLast sql.NullTime
	err = tx.QueryRow("SELECT last_message_time FROM chats WHERE jid = ?", pnChat).Scan(&pnLast)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if pnLast.Valid && (!last.Valid || pnLast.Time.After(last.Time)) {
		last = pnLast
	}
	if _, err := tx.Exec(
		`INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET name = excluded.name, last_message_time = excluded.last_message_time`,
		pnChat, name, last,
	); err != nil {
		return err
	}

	tables := []struct{ name, idColumn string }{{"messages", "id"}}
	if withTranscriptions {
		tables = append(tables, struct{ name, idColumn string }{"transcriptions", "message_id"})
	}
	for _, table := range tables {
		// A message synced under both addresses is kept once
		if _, err := tx.Exec(
			fmt.Sprintf("DELETE FROM %[1]s WHERE chat_jid = ? AND %[2]s IN (SELECT %[2]s FROM %[1]s WHERE chat_jid = ?)", table.name, table.idColumn),
			lidChat, pnChat,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(fmt.Sprintf("UPDATE %s SET chat_jid = ? WHERE chat_jid = ?", table.name), pnChat, lidChat); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("DELETE FROM chats WHERE jid = ?", lidChat); err != nil {
		return err
	}
	return tx.Commit()
}

// moveMedia moves downloaded files to the phone-number chat's folder, where
// downloadMedia looks for them.
func moveMedia(lidChat, pnChat string) error {
	from, to := mediaDir(lidChat), mediaDir(pnChat)
	entries, err := os.ReadDir(from)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0755); err != nil {
		return err
	}
	for _, entry := range entries {
		dest := filepath.Join(to, entry.Name())
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := os.Rename(filepath.Join(from, entry.Name()), dest); err != nil {
			return err
		}
	}
	os.Remove(from) // only succeeds once empty
	return nil
}

// mapLIDSenders rewrites senders stored as LIDs, bare ("123") or as a JID
// ("123@lid"), to the phone number in the same form.
func mapLIDSenders(store *MessageStore, toPN func(types.JID) types.JID) (int, error) {
	rows, err := store.db.Query("SELECT DISTINCT sender FROM messages WHERE sender != ''")
	if err != nil {
		return 0, err
	}
	var senders []string
	for rows.Next() {
		var sender string
		if err := rows.Scan(&sender); err != nil {
			rows.Close()
			return 0, err
		}
		senders = append(senders, sender)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	mapped := 0
	for _, sender := range senders {
		bare := !strings.Contains(sender, "@")
		lid := types.NewJID(sender, types.HiddenUserServer)
		if !bare {
			parsed, err := types.ParseJID(sender)
			if err != nil || parsed.Server != types.HiddenUserServer {
				continue
			}
			lid = parsed
		}
		pn := toPN(lid)
		if pn == lid {
			continue
		}
		replacement := pn.String()
		if bare {
			replacement = pn.User
		}
		if _, err := store.db.Exec("UPDATE messages SET sender = ? WHERE sender = ?", replacement, sender); err != nil {
			return mapped, err
		}
		mapped++
	}
	return mapped, nil
}

func tableExists(db *sql.DB, name string) bool {
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&count)
	return count > 0
}
