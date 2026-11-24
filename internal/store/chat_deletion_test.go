package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

const deletedChatFixture = "15550000001@s.whatsapp.net"

var deletedChatBase = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// seedDeletedChat stores a chat with messages one minute apart, oldest first,
// and returns their ids.
func seedDeletedChat(t *testing.T, db *DB, jid string, n int) []string {
	t.Helper()
	if err := db.UpsertChat(jid, "dm", "Fixture", deletedChatBase); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "m" + string(rune('0'+i))
		if err := db.UpsertMessage(UpsertMessageParams{
			ChatJID: jid, MsgID: ids[i], Timestamp: deletedChatBase.Add(time.Duration(i) * time.Minute),
			Text: "hello", DisplayText: "hello",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func chatDeletedAt(t *testing.T, db *DB, jid string) sql.NullInt64 {
	t.Helper()
	var at sql.NullInt64
	if err := db.sql.QueryRow(`SELECT deleted_at FROM chats WHERE jid = ?`, jid).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func messageDeletion(t *testing.T, db *DB, jid, id string) (deletedAt *time.Time, reason string) {
	t.Helper()
	m, err := db.GetMessage(jid, id)
	if err != nil {
		t.Fatal(err)
	}
	return m.DeletedAt, m.DeletionReason
}

func TestApplyChatDeletionDeletesMessagesThroughRangeAndRecordsChat(t *testing.T) {
	db := openTestDB(t)
	ids := seedDeletedChat(t, db, deletedChatFixture, 3)
	other := seedDeletedChat(t, db, "15550000002@s.whatsapp.net", 1)
	revokedAt := deletedChatBase.Add(30 * time.Second)
	if err := db.UpsertMessage(UpsertMessageParams{
		ChatJID: deletedChatFixture, MsgID: ids[0], Timestamp: deletedChatBase, Revoked: true, DeletedAt: revokedAt,
	}); err != nil {
		t.Fatal(err)
	}
	through := deletedChatBase.Add(time.Minute)
	at := deletedChatBase.Add(10 * time.Minute)

	if err := db.ApplyChatDeletion(ChatDeletion{JIDs: []string{deletedChatFixture}, Through: through, At: at, Chat: true}); err != nil {
		t.Fatal(err)
	}
	if got, reason := messageDeletion(t, db, deletedChatFixture, ids[0]); got == nil || !got.Equal(revokedAt) || reason != MessageDeletionReasonWhatsAppRevoke {
		t.Fatalf("revoked message = %v %q, want its own deletion kept", got, reason)
	}
	if got, reason := messageDeletion(t, db, deletedChatFixture, ids[1]); got == nil || !got.Equal(at) || reason != MessageDeletionReasonChatDeleted {
		t.Fatalf("message at the range end = %v %q, want deleted at %v as %q", got, reason, at, MessageDeletionReasonChatDeleted)
	}
	if got, _ := messageDeletion(t, db, deletedChatFixture, ids[2]); got != nil {
		t.Fatalf("message after the range end deleted at %v, want live", got)
	}
	if got, _ := messageDeletion(t, db, "15550000002@s.whatsapp.net", other[0]); got != nil {
		t.Fatalf("another chat's message deleted at %v", got)
	}
	if got := chatDeletedAt(t, db, deletedChatFixture); !got.Valid || got.Int64 != at.Unix() {
		t.Fatalf("chats.deleted_at = %v, want %d", got, at.Unix())
	}
}

func TestApplyChatDeletionOfAClearedChatDeletesMessagesOnly(t *testing.T) {
	db := openTestDB(t)
	ids := seedDeletedChat(t, db, deletedChatFixture, 2)
	at := deletedChatBase.Add(10 * time.Minute)

	if err := db.ApplyChatDeletion(ChatDeletion{JIDs: []string{deletedChatFixture}, Through: at, At: at}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if got, reason := messageDeletion(t, db, deletedChatFixture, id); got == nil || !got.Equal(at) || reason != MessageDeletionReasonChatCleared {
			t.Fatalf("%s = %v %q, want deleted at %v as %q", id, got, reason, at, MessageDeletionReasonChatCleared)
		}
	}
	if got := chatDeletedAt(t, db, deletedChatFixture); got.Valid {
		t.Fatalf("a cleared chat has chats.deleted_at = %d, want NULL", got.Int64)
	}
}

func TestApplyChatDeletionCoversEveryJIDOfTheChat(t *testing.T) {
	db := openTestDB(t)
	lid := "99990000001@lid"
	seedDeletedChat(t, db, deletedChatFixture, 1)
	seedDeletedChat(t, db, lid, 1)
	at := deletedChatBase.Add(10 * time.Minute)

	if err := db.ApplyChatDeletion(ChatDeletion{JIDs: []string{deletedChatFixture, lid}, Through: at, At: at, Chat: true}); err != nil {
		t.Fatal(err)
	}
	for _, jid := range []string{deletedChatFixture, lid} {
		if got, _ := messageDeletion(t, db, jid, "m0"); got == nil {
			t.Fatalf("%s message kept live", jid)
		}
		if got := chatDeletedAt(t, db, jid); !got.Valid {
			t.Fatalf("%s not recorded deleted", jid)
		}
	}
}

// A full app-state sync hands over every delete still in the collection, old
// ones included. One the chat has outlived must not take it away again.
func TestApplyChatDeletionKeepsAChatWithANewerMessage(t *testing.T) {
	db := openTestDB(t)
	ids := seedDeletedChat(t, db, deletedChatFixture, 3)
	at := deletedChatBase.Add(90 * time.Second)

	if err := db.ApplyChatDeletion(ChatDeletion{JIDs: []string{deletedChatFixture}, Through: deletedChatBase.Add(time.Minute), At: at, Chat: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := messageDeletion(t, db, deletedChatFixture, ids[1]); got == nil {
		t.Fatal("message in the range kept live")
	}
	if got := chatDeletedAt(t, db, deletedChatFixture); got.Valid {
		t.Fatalf("chat with a message after the delete recorded deleted at %d", got.Int64)
	}
}

func TestNewerMessageClearsChatDeleted(t *testing.T) {
	db := openTestDB(t)
	seedDeletedChat(t, db, deletedChatFixture, 1)
	at := deletedChatBase.Add(10 * time.Minute)
	if err := db.ApplyChatDeletion(ChatDeletion{JIDs: []string{deletedChatFixture}, Through: at, At: at, Chat: true}); err != nil {
		t.Fatal(err)
	}

	// Neither an older message history sync brings, nor a reaction, nor a
	// system placeholder, nor a deletion's own row brings the chat back.
	for _, m := range []UpsertMessageParams{
		{ChatJID: deletedChatFixture, MsgID: "history", Timestamp: at, Text: "older", DisplayText: "older"},
		{ChatJID: deletedChatFixture, MsgID: "reaction", Timestamp: at.Add(time.Minute), ReactionToID: "m0", ReactionEmoji: "👍"},
		{ChatJID: deletedChatFixture, MsgID: "system", Timestamp: at.Add(time.Minute), DisplayText: "(message)"},
		{ChatJID: deletedChatFixture, MsgID: "gone", Timestamp: at.Add(time.Minute), DeletedForMe: true, DeletedAt: at.Add(time.Minute)},
	} {
		if err := db.UpsertMessage(m); err != nil {
			t.Fatal(err)
		}
		if got := chatDeletedAt(t, db, deletedChatFixture); !got.Valid {
			t.Fatalf("%s cleared chats.deleted_at", m.MsgID)
		}
	}

	if err := db.UpsertMessage(UpsertMessageParams{ChatJID: deletedChatFixture, MsgID: "new", Timestamp: at.Add(time.Second), Text: "hi again", DisplayText: "hi again"}); err != nil {
		t.Fatal(err)
	}
	if got := chatDeletedAt(t, db, deletedChatFixture); got.Valid {
		t.Fatalf("a newer message left chats.deleted_at = %d", got.Int64)
	}
}

func TestChatDeletedMigrationOnAnExistingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wacli.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedDeletedChat(t, db, deletedChatFixture, 1)
	if _, err := db.sql.Exec(`DROP TRIGGER chats_deleted_cleared;
 ALTER TABLE chats DROP COLUMN deleted_at;
 DELETE FROM schema_migrations WHERE version = 35`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := chatDeletedAt(t, db, deletedChatFixture); got.Valid {
		t.Fatalf("migrated chat has deleted_at = %d, want NULL", got.Int64)
	}
	at := deletedChatBase.Add(10 * time.Minute)
	if err := db.ApplyChatDeletion(ChatDeletion{JIDs: []string{deletedChatFixture}, Through: at, At: at, Chat: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessage(UpsertMessageParams{ChatJID: deletedChatFixture, MsgID: "new", Timestamp: at.Add(time.Second), Text: "hi", DisplayText: "hi"}); err != nil {
		t.Fatal(err)
	}
	if got := chatDeletedAt(t, db, deletedChatFixture); got.Valid {
		t.Fatal("the migrated store's trigger did not clear chats.deleted_at")
	}
}
