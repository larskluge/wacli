package store

import (
	"fmt"
	"strings"

	"github.com/openclaw/wacli/internal/store/storedb"
)

// MessageContact is one contact card a message shared, kept as WhatsApp
// delivered it: the display name and the vCard, byte for byte.
type MessageContact struct {
	Position    int
	DisplayName string
	VCard       string
}

// ReplaceMessageContacts makes a message's stored cards exactly contacts, each
// at its index. A purged message keeps none.
func (d *DB) ReplaceMessageContacts(chatJID, msgID string, contacts []MessageContact) error {
	if d == nil {
		return fmt.Errorf("nil db")
	}
	if strings.TrimSpace(chatJID) == "" || strings.TrimSpace(msgID) == "" {
		return fmt.Errorf("message contacts require chat_jid and msg_id")
	}
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := d.q.WithTx(tx)
	ctx := storeCtx()
	if err := q.DeleteMessageContacts(ctx, storedb.DeleteMessageContactsParams{ChatJid: chatJID, MsgID: msgID}); err != nil {
		return fmt.Errorf("replace message contacts: %w", err)
	}
	for _, c := range contacts {
		if err := q.InsertMessageContact(ctx, storedb.InsertMessageContactParams{
			ChatJid:     chatJID,
			MsgID:       msgID,
			Position:    int64(c.Position),
			DisplayName: nullStringIfEmpty(c.DisplayName),
			Vcard:       nullStringIfEmpty(c.VCard),
			ChatJid_2:   chatJID,
			MsgID_2:     msgID,
		}); err != nil {
			return fmt.Errorf("insert message contact %d: %w", c.Position, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replace message contacts: %w", err)
	}
	return nil
}

// ListMessageContacts returns a message's stored cards in position order.
func (d *DB) ListMessageContacts(chatJID, msgID string) ([]MessageContact, error) {
	if d == nil {
		return nil, fmt.Errorf("nil db")
	}
	rows, err := d.q.ListMessageContacts(storeCtx(), storedb.ListMessageContactsParams{ChatJid: chatJID, MsgID: msgID})
	if err != nil {
		return nil, fmt.Errorf("list message contacts: %w", err)
	}
	out := make([]MessageContact, 0, len(rows))
	for _, row := range rows {
		out = append(out, MessageContact{Position: int(row.Position), DisplayName: row.DisplayName, VCard: row.Vcard})
	}
	return out, nil
}
