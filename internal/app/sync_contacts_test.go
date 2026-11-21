package app

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openclaw/wacli/internal/store"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// Fictional cards. CRLF line ends and the trailing break are WhatsApp's own and
// must survive storage untouched.
const (
	marigoldVCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Quill;Marigold;;;\r\nFN:Marigold Quill\r\n" +
		"item1.TEL;waid=15550101234:+1 555-010-1234\r\nitem1.X-ABLabel:Mobile\r\nEND:VCARD\r\n"
	tobiasVCard = "BEGIN:VCARD\nVERSION:3.0\nFN:Tobias Fenn\nTEL;type=CELL;waid=15550109876:+1 555-010-9876\n" +
		"item1.TEL;waid=15550104321:+1 555-010-4321\nEND:VCARD"
)

func TestSyncStoresSharedContactCards(t *testing.T) {
	cases := []struct {
		name     string
		msg      *waProto.Message
		wantText string
		want     []store.MessageContact
	}{
		{
			name: "single card",
			msg: &waProto.Message{ContactMessage: &waProto.ContactMessage{
				DisplayName: proto.String("Marigold Quill"),
				Vcard:       proto.String(marigoldVCard),
			}},
			wantText: "Contact: Marigold Quill (+1 555-010-1234)",
			want:     []store.MessageContact{{Position: 0, DisplayName: "Marigold Quill", VCard: marigoldVCard}},
		},
		{
			name: "two-card array",
			msg: &waProto.Message{ContactsArrayMessage: &waProto.ContactsArrayMessage{
				DisplayName: proto.String("2 contacts"),
				Contacts: []*waProto.ContactMessage{
					{DisplayName: proto.String("Marigold Quill"), Vcard: proto.String(marigoldVCard)},
					{DisplayName: proto.String("Tobias Fenn"), Vcard: proto.String(tobiasVCard)},
				},
			}},
			wantText: "Contacts:\nContact: Marigold Quill (+1 555-010-1234)\nContact: Tobias Fenn (+1 555-010-9876, +1 555-010-4321)",
			want: []store.MessageContact{
				{Position: 0, DisplayName: "Marigold Quill", VCard: marigoldVCard},
				{Position: 1, DisplayName: "Tobias Fenn", VCard: tobiasVCard},
			},
		},
		{
			name:     "not a contact",
			msg:      &waProto.Message{Conversation: proto.String("see you at nine")},
			wantText: "see you at nine",
			want:     []store.MessageContact{},
		},
	}
	for _, tc := range cases {
		for _, mode := range []string{"live", "history"} {
			for _, fromMe := range []bool{false, true} {
				name := tc.name + "/" + mode
				if fromMe {
					name += "/from me"
				}
				t.Run(name, func(t *testing.T) {
					a := newTestApp(t)
					a.wa = newFakeWA()
					chat := types.JID{User: "15550100000", Server: types.DefaultUserServer}
					stamp := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
					const id = "CONTACT-FIXTURE"

					// The second pass re-stores the same message: its rows stay as they were.
					for pass := 1; pass <= 2; pass++ {
						var stored, last atomic.Int64
						if mode == "live" {
							evt := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat, IsFromMe: fromMe}, ID: id, Timestamp: stamp}, Message: tc.msg}
							a.handleLiveSyncMessage(context.Background(), SyncOptions{}, evt, &stored, nil, nil)
						} else {
							msg := &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{ID: proto.String(id), RemoteJID: proto.String(chat.String()), FromMe: proto.Bool(fromMe)}, MessageTimestamp: proto.Uint64(uint64(stamp.Unix())), Message: tc.msg}
							hist := &events.HistorySync{Data: &waHistorySync.HistorySync{Conversations: []*waHistorySync.Conversation{{ID: proto.String(chat.String()), Messages: []*waHistorySync.HistorySyncMsg{{Message: msg}}}}}}
							a.handleHistorySync(context.Background(), SyncOptions{}, hist, &stored, &last, nil)
						}
						if stored.Load() != 1 {
							t.Fatalf("pass %d stored %d messages, want 1", pass, stored.Load())
						}
						got, err := a.db.ListMessageContacts(chat.String(), id)
						if err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(got, tc.want) {
							t.Fatalf("pass %d contacts = %#v, want %#v", pass, got, tc.want)
						}
						row, err := a.db.GetMessage(chat.String(), id)
						if err != nil {
							t.Fatal(err)
						}
						if row.Text != tc.wantText || row.FromMe != fromMe {
							t.Fatalf("pass %d message text=%q from_me=%v, want %q %v", pass, row.Text, row.FromMe, tc.wantText, fromMe)
						}
					}
				})
			}
		}
	}
}
