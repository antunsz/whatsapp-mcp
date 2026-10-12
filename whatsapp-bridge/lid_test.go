package main

import (
	"os"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestMergeLIDChats(t *testing.T) {
	t.Chdir(t.TempDir())
	store, err := NewMessageStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	sunday := time.Date(2026, 10, 11, 12, 18, 0, 0, time.UTC)
	monday := sunday.Add(28 * time.Hour)
	must(store.StoreChat("555@s.whatsapp.net", "Bernardo", sunday))
	must(store.StoreChat("111@lid", "111", monday))
	must(store.StoreChat("999@lid", "999", monday))
	must(store.StoreChat("1@g.us", "Grupo", monday))
	text := func(id, chat, sender, content string, at time.Time) {
		must(store.StoreMessage(id, chat, sender, content, at, false, "", "", "", nil, nil, nil, 0))
	}
	text("OLD", "555@s.whatsapp.net", "555", "domingo", sunday)
	text("BOTH", "555@s.whatsapp.net", "555", "sincronizada duas vezes", sunday)
	text("BOTH", "111@lid", "111", "sincronizada duas vezes", sunday)
	must(store.StoreMessage("NEW", "111@lid", "111", "", monday, false, "audio", "audio.ogg", "", nil, nil, nil, 0))
	text("G1", "1@g.us", "111", "lid sem servidor", monday)
	text("G2", "1@g.us", "111@lid", "lid com servidor", monday)
	text("U", "999@lid", "999", "lid sem número conhecido", monday)

	_, err = store.db.Exec(`CREATE TABLE transcriptions (message_id TEXT, chat_jid TEXT, text TEXT, PRIMARY KEY (message_id, chat_jid));
		INSERT INTO transcriptions VALUES ('NEW', '111@lid', 'te dar um exemplo')`)
	must(err)
	must(os.MkdirAll("store/111@lid", 0755))
	must(os.WriteFile("store/111@lid/NEW_audio.ogg", []byte("ogg"), 0644))

	toPN := func(jid types.JID) types.JID {
		if jid == types.NewJID("111", types.HiddenUserServer) {
			return types.NewJID("555", types.DefaultUserServer)
		}
		return jid
	}
	chatName := func(types.JID) string { return "Bernardo Satt" }
	logger := waLog.Noop

	// Twice: the second run must find nothing left to do
	for range 2 {
		must(mergeLIDChats(store, toPN, chatName, logger))
	}

	query := func(q string, args ...any) string {
		t.Helper()
		var out string
		must(store.db.QueryRow(q, args...).Scan(&out))
		return out
	}
	expect := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: got %q, want %q", what, got, want)
		}
	}
	expect("LID chats left", query("SELECT group_concat(jid) FROM chats WHERE jid LIKE '%@lid'"), "999@lid")
	expect("name", query("SELECT name FROM chats WHERE jid = '555@s.whatsapp.net'"), "Bernardo Satt")
	var last time.Time
	must(store.db.QueryRow("SELECT last_message_time FROM chats WHERE jid = '555@s.whatsapp.net'").Scan(&last))
	if !last.Equal(monday) {
		t.Errorf("last message time: got %v, want %v", last, monday)
	}
	expect("messages", query("SELECT group_concat(id, ',') FROM (SELECT id FROM messages WHERE chat_jid = '555@s.whatsapp.net' ORDER BY id)"), "BOTH,NEW,OLD")
	expect("senders", query("SELECT group_concat(sender, ',') FROM (SELECT sender FROM messages WHERE chat_jid = '1@g.us' ORDER BY id)"), "555,555@s.whatsapp.net")
	expect("transcription", query("SELECT chat_jid FROM transcriptions WHERE message_id = 'NEW'"), "555@s.whatsapp.net")
	expect("unmapped chat", query("SELECT chat_jid FROM messages WHERE id = 'U'"), "999@lid")
	if _, err := os.Stat("store/555@s.whatsapp.net/NEW_audio.ogg"); err != nil {
		t.Errorf("media not moved: %v", err)
	}
}
