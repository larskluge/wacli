package wa

import (
	"reflect"
	"testing"
	"time"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// iPhone contact cards group each number with its label ("item1.TEL" +
// "item1.X-ABLabel"); the grouped TEL used to be ignored, losing the number.
func TestContactDisplayTextReadsGroupedVCardProperties(t *testing.T) {
	vcard := "BEGIN:VCARD\r\n" +
		"VERSION:3.0\r\n" +
		"N:Rossi;Mario;;;\r\n" +
		"item1.FN:Mario Rossi\r\n" +
		"item1.TEL;type=CELL;type=VOICE;waid=393331234567:+39 333 123 4567\r\n" +
		"item1.X-ABLabel:Mobile\r\n" +
		"item2.TEL;type=WORK:+39 02 1234567\r\n" +
		"item2.X-ABLabel:Club\r\n" +
		"END:VCARD"

	got := contactDisplayText(&waProto.ContactMessage{Vcard: proto.String(vcard)})
	want := "Contact: Mario Rossi (+39 333 123 4567, +39 02 1234567)"
	if got != want {
		t.Fatalf("contactDisplayText = %q, want %q", got, want)
	}
}

func TestContactDisplayTextKeepsDisplayNameOverGroupedFN(t *testing.T) {
	got := contactDisplayText(&waProto.ContactMessage{
		DisplayName: proto.String("Referente Club"),
		Vcard:       proto.String("BEGIN:VCARD\nitem1.FN:Mario Rossi\nitem1.TEL:+39 333 1234567\nEND:VCARD"),
	})
	if got != "Contact: Referente Club (+39 333 1234567)" {
		t.Fatalf("contactDisplayText = %q", got)
	}
}

func TestParseKeepsContactCardsWhole(t *testing.T) {
	// Fictional cards; the padding and line ends are kept as sent.
	const (
		vcardA = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Marigold Quill\r\nitem1.TEL;waid=15550101234:+1 555-010-1234\r\nEND:VCARD\r\n"
		vcardB = "BEGIN:VCARD\nFN:Tobias Fenn\nitem1.TEL;waid=15550109876:+1 555-010-9876\nEND:VCARD"
	)
	cases := []struct {
		name string
		msg  *waProto.Message
		want []Contact
	}{
		{
			name: "single card",
			msg:  &waProto.Message{ContactMessage: &waProto.ContactMessage{DisplayName: proto.String(" Marigold Quill "), Vcard: proto.String(vcardA)}},
			want: []Contact{{DisplayName: " Marigold Quill ", VCard: vcardA}},
		},
		{
			name: "array keeps order",
			msg: &waProto.Message{ContactsArrayMessage: &waProto.ContactsArrayMessage{
				DisplayName: proto.String("2 contacts"),
				Contacts: []*waProto.ContactMessage{
					{DisplayName: proto.String("Marigold Quill"), Vcard: proto.String(vcardA)},
					{DisplayName: proto.String("Tobias Fenn"), Vcard: proto.String(vcardB)},
				},
			}},
			want: []Contact{{DisplayName: "Marigold Quill", VCard: vcardA}, {DisplayName: "Tobias Fenn", VCard: vcardB}},
		},
		{
			name: "array with no cards",
			msg:  &waProto.Message{ContactsArrayMessage: &waProto.ContactsArrayMessage{DisplayName: proto.String("0 contacts")}},
		},
		{
			name: "not a contact",
			msg:  &waProto.Message{Conversation: proto.String("hello")},
		},
	}
	chat := types.JID{User: "15550100000", Server: types.DefaultUserServer}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := ParseLiveMessage(&events.Message{
				Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, Sender: chat}, ID: "c1", Timestamp: time.Unix(1700000000, 0)},
				Message: tc.msg,
			})
			if !reflect.DeepEqual(live.Contacts, tc.want) {
				t.Fatalf("contacts = %#v, want %#v", live.Contacts, tc.want)
			}
		})
	}
}
