package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Fictional cards.
var (
	cardMarigold = MessageContact{Position: 0, DisplayName: "Marigold Quill", VCard: "BEGIN:VCARD\r\nFN:Marigold Quill\r\nitem1.TEL;waid=15550101234:+1 555-010-1234\r\nEND:VCARD\r\n"}
	cardTobias   = MessageContact{Position: 1, DisplayName: "Tobias Fenn", VCard: "BEGIN:VCARD\nFN:Tobias Fenn\nitem1.TEL;waid=15550109876:+1 555-010-9876\nEND:VCARD"}
)

func storeContactMessage(t *testing.T, db *DB, chat, id string, ts time.Time, cards ...MessageContact) {
	t.Helper()
	if err := db.UpsertChat(chat, "dm", "Fixture", ts); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessage(UpsertMessageParams{ChatJID: chat, MsgID: id, Timestamp: ts, Text: "Contact: Marigold Quill"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceMessageContacts(chat, id, cards); err != nil {
		t.Fatal(err)
	}
}

func messageContacts(t *testing.T, db *DB, chat, id string) []MessageContact {
	t.Helper()
	got, err := db.ListMessageContacts(chat, id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReplaceMessageContactsReplacesTheMessagesRows(t *testing.T) {
	db := openTestDB(t)
	chat := "15550100000@s.whatsapp.net"
	storeContactMessage(t, db, chat, "C1", time.Unix(1700000000, 0), cardMarigold, cardTobias)
	storeContactMessage(t, db, chat, "C2", time.Unix(1700000000, 0), cardMarigold)

	changed := MessageContact{Position: 0, DisplayName: "Marigold Q.", VCard: "BEGIN:VCARD\nFN:Marigold Q.\nEND:VCARD"}
	if err := db.ReplaceMessageContacts(chat, "C1", []MessageContact{changed}); err != nil {
		t.Fatal(err)
	}
	if got := messageContacts(t, db, chat, "C1"); !reflect.DeepEqual(got, []MessageContact{changed}) {
		t.Fatalf("C1 contacts = %#v", got)
	}
	if got := messageContacts(t, db, chat, "C2"); !reflect.DeepEqual(got, []MessageContact{cardMarigold}) {
		t.Fatalf("another message's contacts changed: %#v", got)
	}
}

func TestReplaceMessageContactsRequiresIdentity(t *testing.T) {
	db := openTestDB(t)
	if err := db.ReplaceMessageContacts(" ", "C1", []MessageContact{cardMarigold}); err == nil {
		t.Fatal("blank chat JID accepted")
	}
	if err := db.ReplaceMessageContacts("15550100000@s.whatsapp.net", "", []MessageContact{cardMarigold}); err == nil {
		t.Fatal("blank message ID accepted")
	}
}

func TestPurgeMessageDropsContactsAndKeepsThemOut(t *testing.T) {
	db := openTestDB(t)
	chat := "15550100000@s.whatsapp.net"
	storeContactMessage(t, db, chat, "C1", time.Unix(1700000000, 0), cardMarigold, cardTobias)
	if err := db.MarkMessageRevoked(chat, "C1"); err != nil {
		t.Fatal(err)
	}
	if err := db.PurgeMessage(chat, "C1"); err != nil {
		t.Fatal(err)
	}
	if got := messageContacts(t, db, chat, "C1"); len(got) != 0 {
		t.Fatalf("purged message kept its contacts: %#v", got)
	}
	// A later sync echo of the purged message must not bring the cards back.
	if err := db.ReplaceMessageContacts(chat, "C1", []MessageContact{cardMarigold}); err != nil {
		t.Fatal(err)
	}
	if got := messageContacts(t, db, chat, "C1"); len(got) != 0 {
		t.Fatalf("purged message's contacts resurfaced: %#v", got)
	}
}

func TestDeleteChatPathsDropTheirMessageContacts(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name   string
		chat   string
		ts     time.Time
		delete func(*DB, string) error
	}{
		{name: "delete chat", chat: "15550100000@s.whatsapp.net", ts: now, delete: (*DB).DeleteChat},
		{name: "delete group local data", chat: "120363000000000000@g.us", ts: now, delete: (*DB).DeleteGroupLocalData},
		{name: "delete stale chats", chat: "15550100000@s.whatsapp.net", ts: now.AddDate(0, 0, -200), delete: func(db *DB, _ string) error {
			_, err := db.DeleteChatsOlderThan(180)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			other := "15550109999@s.whatsapp.net"
			storeContactMessage(t, db, tc.chat, "C1", tc.ts, cardMarigold)
			storeContactMessage(t, db, other, "C1", now, cardMarigold)
			if err := tc.delete(db, tc.chat); err != nil {
				t.Fatal(err)
			}
			if got := messageContacts(t, db, tc.chat, "C1"); len(got) != 0 {
				t.Fatalf("deleted chat kept its contacts: %#v", got)
			}
			if got := messageContacts(t, db, other, "C1"); len(got) != 1 {
				t.Fatalf("another chat lost its contacts: %#v", got)
			}
		})
	}
}

func TestMigrateLIDToPNMovesMessageContacts(t *testing.T) {
	for _, purged := range []bool{false, true} {
		db := openTestDB(t)
		pn := "15551234567@s.whatsapp.net"
		lid := "999123456789@lid"
		storeContactMessage(t, db, lid, "C1", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), cardMarigold, cardTobias)
		if purged {
			if err := db.MarkMessageRevoked(lid, "C1"); err != nil {
				t.Fatal(err)
			}
			if err := db.PurgeMessage(lid, "C1"); err != nil {
				t.Fatal(err)
			}
		}
		if err := db.MigrateLIDToPN(lid, pn); err != nil {
			t.Fatalf("MigrateLIDToPN: %v", err)
		}
		want := []MessageContact{cardMarigold, cardTobias}
		if purged {
			want = []MessageContact{}
		}
		if got := messageContacts(t, db, pn, "C1"); !reflect.DeepEqual(got, want) {
			t.Fatalf("purged=%v phone contacts = %#v, want %#v", purged, got, want)
		}
		if got := messageContacts(t, db, lid, "C1"); len(got) != 0 {
			t.Fatalf("purged=%v LID contacts retained: %#v", purged, got)
		}
	}
}

func TestOpenRepairsRecordedMessageContactsMigrationMissingTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wacli.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE message_contacts`); err != nil {
		_ = raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatalf("Open repaired DB: %v", err)
	}
	defer db.Close()
	if ok, err := db.tableExists("message_contacts"); err != nil || !ok {
		t.Fatalf("message_contacts exists=%v err=%v", ok, err)
	}
}
