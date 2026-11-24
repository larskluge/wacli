package app

import (
	"context"
	"fmt"
	"time"

	"github.com/openclaw/wacli/internal/store"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// handleChatDeletionEvent records a chat this account deleted or cleared on
// another device, or through this one (whatsmeow hands a sent patch back as
// its event): the messages through the action's range end are deleted, and a
// deleted chat is recorded in chats.deleted_at (store.ApplyChatDeletion).
func (a *App) handleChatDeletionEvent(ctx context.Context, evt any) error {
	var (
		jid     types.JID
		at      time.Time
		rng     *waSyncAction.SyncActionMessageRange
		deleted bool
	)
	switch v := evt.(type) {
	case *events.DeleteChat:
		if v == nil {
			return nil
		}
		jid, at, rng, deleted = v.JID, v.Timestamp, v.Action.GetMessageRange(), true
	case *events.ClearChat:
		if v == nil {
			return nil
		}
		jid, at, rng = v.JID, v.Timestamp, v.Action.GetMessageRange()
	default:
		return nil
	}
	kind := "clear"
	if deleted {
		kind = "delete"
	}
	if jid.IsEmpty() {
		return nil
	}
	// The range end, else the action's own time: whatsmeow writes the range
	// end in whole seconds, as the store keeps message times.
	through := at
	if ts := rng.GetLastMessageTimestamp(); ts > 0 {
		through = time.Unix(ts, 0).UTC()
	}
	if at.IsZero() || through.IsZero() {
		a.emitWarning(
			"chat_deletion_without_time",
			fmt.Sprintf("warning: chat %s for %s carries no time; nothing was deleted", kind, jid),
			map[string]any{"kind": kind, "jid": jid.String()},
		)
		return nil
	}
	if err := a.db.ApplyChatDeletion(store.ChatDeletion{
		JIDs:    a.chatStoreJIDs(ctx, jid),
		Through: through,
		At:      at,
		Chat:    deleted,
	}); err != nil {
		a.emitChatStateWarning(kind, jid, err)
		return err
	}
	return nil
}

// chatStoreJIDs is every JID the store may hold a chat under: a one-to-one
// chat's phone-number and LID forms, or a group's or channel's own JID.
func (a *App) chatStoreJIDs(ctx context.Context, jid types.JID) []string {
	if jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer {
		return a.chatDeleteJIDs(ctx, jid.ToNonAD())
	}
	return []string{canonicalJIDString(a.canonicalStoreJID(ctx, jid))}
}
