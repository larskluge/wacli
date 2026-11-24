package store

import (
	"fmt"
	"strings"
	"time"
)

// ChatDeletion is a chat this account deleted or cleared on another of its
// devices, as WhatsApp's app state reports it (whatsmeow's DeleteChat and
// ClearChat events).
type ChatDeletion struct {
	// JIDs is every JID the store may hold the chat under: its phone-number
	// and LID forms.
	JIDs []string
	// Through is the end of the action's message range: the messages at or
	// before it go, and a later one stays, as it does on the phone.
	Through time.Time
	// At is when the account deleted or cleared the chat.
	At time.Time
	// Chat is whether the chat itself went (DeleteChat), not only its
	// messages (ClearChat).
	Chat bool
}

// ApplyChatDeletion marks the chat's live messages at or before Through
// deleted at At, as chat_deleted or chat_cleared; a message deleted already
// keeps its own deletion. A deleted chat is recorded in chats.deleted_at as
// well, unless the store holds a message later than At: a full app-state sync
// hands over old deletes too, and a chat that has had a message since is back.
// A later message clears chats.deleted_at again (the chats_deleted_cleared
// trigger, migration 35).
func (d *DB) ApplyChatDeletion(del ChatDeletion) error {
	if len(del.JIDs) == 0 {
		return fmt.Errorf("chat JID is required")
	}
	if del.Through.IsZero() || del.At.IsZero() {
		return fmt.Errorf("a chat deletion needs its range end and its time")
	}
	reason := MessageDeletionReasonChatCleared
	if del.Chat {
		reason = MessageDeletionReasonChatDeleted
	}
	in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(del.JIDs)), ",") + ")"
	jids := make([]any, len(del.JIDs))
	for i, jid := range del.JIDs {
		jids[i] = jid
	}
	at := unix(del.At)

	tx, err := d.sql.BeginTx(storeCtx(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		"UPDATE messages SET deleted_at = ?, deletion_reason = ? WHERE chat_jid IN "+in+" AND ts <= ? AND deleted_at IS NULL",
		append(append([]any{at, reason}, jids...), unix(del.Through))...,
	); err != nil {
		return fmt.Errorf("delete the chat's messages: %w", err)
	}
	if del.Chat {
		args := append(append([]any{at}, jids...), at)
		args = append(append(args, jids...), at)
		if _, err := tx.Exec(
			"UPDATE chats SET deleted_at = ? WHERE jid IN "+in+" AND COALESCE(deleted_at, 0) < ?"+
				" AND NOT EXISTS (SELECT 1 FROM messages WHERE chat_jid IN "+in+" AND ts > ? AND deleted_at IS NULL"+
				" AND COALESCE(reaction_to_id,'') = '' AND COALESCE(reaction_emoji,'') = ''"+
				" AND (COALESCE(display_text,'') != '(message)' OR TRIM(COALESCE(text,'')) != ''))",
			args...,
		); err != nil {
			return fmt.Errorf("record the chat deleted: %w", err)
		}
	}
	return tx.Commit()
}

// migrateChatDeletedAt adds chats.deleted_at, when the account deleted the
// chat (ApplyChatDeletion), and the trigger that clears it once a later
// message is stored there: the chat is back on the phone then. A reaction, a
// content-free "(message)" system row or a row stored deleted brings nothing
// back.
func migrateChatDeletedAt(d *DB) error {
	hasChats, err := d.tableExists("chats")
	if err != nil || !hasChats {
		return err
	}
	exists, err := d.tableHasColumn("chats", "deleted_at")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := d.sql.Exec(`ALTER TABLE chats ADD COLUMN deleted_at INTEGER`); err != nil {
			return fmt.Errorf("add chats.deleted_at column: %w", err)
		}
	}
	hasMessages, err := d.tableExists("messages")
	if err != nil || !hasMessages {
		return err
	}
	if _, err := d.sql.Exec(`CREATE TRIGGER IF NOT EXISTS chats_deleted_cleared AFTER INSERT ON messages
 WHEN new.deleted_at IS NULL
  AND COALESCE(new.reaction_to_id,'') = '' AND COALESCE(new.reaction_emoji,'') = ''
  AND (COALESCE(new.display_text,'') != '(message)' OR TRIM(COALESCE(new.text,'')) != '')
 BEGIN
  UPDATE chats SET deleted_at = NULL WHERE jid = new.chat_jid AND deleted_at IS NOT NULL AND new.ts > deleted_at;
 END`); err != nil {
		return fmt.Errorf("create chats_deleted_cleared trigger: %w", err)
	}
	return nil
}
