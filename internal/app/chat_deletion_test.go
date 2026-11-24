package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclaw/wacli/internal/store"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var chatDeletionBase = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// seedChatDeletion stores a DM with three messages a minute apart: m0, m1, m2.
func seedChatDeletion(t *testing.T, a *App, chat types.JID) {
	t.Helper()
	if err := a.db.UpsertChat(chat.String(), "dm", "Fixture", chatDeletionBase); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"m0", "m1", "m2"} {
		if err := a.db.UpsertMessage(store.UpsertMessageParams{
			ChatJID: chat.String(), MsgID: id, Timestamp: chatDeletionBase.Add(time.Duration(i) * time.Minute),
			Text: "hello", DisplayText: "hello",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func deleteChatEvent(chat types.JID, at time.Time, through time.Time) *events.DeleteChat {
	return &events.DeleteChat{JID: chat, Timestamp: at, Action: &waSyncAction.DeleteChatAction{
		MessageRange: &waSyncAction.SyncActionMessageRange{LastMessageTimestamp: proto.Int64(through.Unix())},
	}}
}

// assertChatDeletion checks which of m0..m2 are deleted, and how, and the
// chat's deleted_at (0 for NULL).
func assertChatDeletion(t *testing.T, a *App, chat types.JID, reason string, deleted []bool, at time.Time, chatDeletedAt int64) {
	t.Helper()
	for i, id := range []string{"m0", "m1", "m2"} {
		m, err := a.db.GetMessage(chat.String(), id)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case deleted[i] && (m.DeletedAt == nil || !m.DeletedAt.Equal(at) || m.DeletionReason != reason):
			t.Errorf("%s %s = %v %q, want deleted at %v as %q", chat, id, m.DeletedAt, m.DeletionReason, at, reason)
		case !deleted[i] && m.DeletedAt != nil:
			t.Errorf("%s %s deleted at %v, want live", chat, id, m.DeletedAt)
		}
	}
	raw, err := sql.Open("sqlite3", filepath.Join(a.StoreDir(), "wacli.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var got sql.NullInt64
	if err := raw.QueryRow(`SELECT deleted_at FROM chats WHERE jid = ?`, chat.String()).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got.Int64 != chatDeletedAt {
		t.Errorf("%s chats.deleted_at = %v, want %d", chat, got, chatDeletedAt)
	}
}

func TestSyncRecordsAChatDeletedOrClearedOnAnotherDevice(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f
	deleted := types.JID{User: "15550000001", Server: types.DefaultUserServer}
	cleared := types.JID{User: "15550000002", Server: types.DefaultUserServer}
	seedChatDeletion(t, a, deleted)
	seedChatDeletion(t, a, cleared)
	at := chatDeletionBase.Add(10 * time.Minute)

	_, err := a.Sync(context.Background(), SyncOptions{Mode: SyncModeOnce, IdleExit: time.Millisecond, AfterConnect: func(context.Context) error {
		// Deleted through m1: m2 stays, as it does on the phone. The chat is
		// recorded deleted all the same: nothing is later than the delete.
		f.emit(deleteChatEvent(deleted, at, chatDeletionBase.Add(time.Minute)))
		// Cleared with no message range: through the event's own time.
		f.emit(&events.ClearChat{JID: cleared, Timestamp: at, Action: &waSyncAction.ClearChatAction{}})
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.appStatePersist.waitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertChatDeletion(t, a, deleted, store.MessageDeletionReasonChatDeleted, []bool{true, true, false}, at, at.Unix())
	assertChatDeletion(t, a, cleared, store.MessageDeletionReasonChatCleared, []bool{true, true, true}, at, 0)
}

func TestSyncRecordsTheChatItselfDeleted(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f
	chat := types.JID{User: "15550000001", Server: types.DefaultUserServer}
	seedChatDeletion(t, a, chat)
	at := chatDeletionBase.Add(10 * time.Minute)

	_, err := a.Sync(context.Background(), SyncOptions{Mode: SyncModeOnce, IdleExit: time.Millisecond, AfterConnect: func(context.Context) error {
		f.emit(deleteChatEvent(chat, at, chatDeletionBase.Add(2*time.Minute)))
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.appStatePersist.waitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertChatDeletion(t, a, chat, store.MessageDeletionReasonChatDeleted, []bool{true, true, true}, at, at.Unix())

	// Somebody writes again: the chat is back, its old messages stay deleted.
	if err := a.db.UpsertMessage(store.UpsertMessageParams{
		ChatJID: chat.String(), MsgID: "m3", Timestamp: at.Add(time.Minute), Text: "hi again", DisplayText: "hi again",
	}); err != nil {
		t.Fatal(err)
	}
	assertChatDeletion(t, a, chat, store.MessageDeletionReasonChatDeleted, []bool{true, true, true}, at, 0)
}

func TestChatStatePersistenceHandlerRecordsAChatDeletion(t *testing.T) {
	a := newTestApp(t)
	f := newFakeWA()
	a.wa = f
	chat := types.JID{User: "15550000001", Server: types.DefaultUserServer}
	seedChatDeletion(t, a, chat)
	at := chatDeletionBase.Add(10 * time.Minute)
	f.connectEvents = []any{deleteChatEvent(chat, at, chatDeletionBase.Add(2*time.Minute))}

	removeHandler, err := a.AddChatStatePersistenceHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	removeHandler()
	if err := a.appStatePersist.waitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertChatDeletion(t, a, chat, store.MessageDeletionReasonChatDeleted, []bool{true, true, true}, at, at.Unix())
}
