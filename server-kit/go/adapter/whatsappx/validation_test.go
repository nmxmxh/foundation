package whatsappx

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMessageValidationBoundaries(t *testing.T) {
	valid := []Message{NewTextMessage("2348012345678", strings.Repeat("Ọ", 4096)), NewLocation("2348012345678", 90, -180, "Ọlá", "Lagos"), NewQuickReply("2348012345678", "Choose", "Ọlá"), NewTemplateMessage("2348012345678", "name", "en", "value"), NewAudioMessage("2348012345678", "media")}
	for _, m := range valid {
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []Message{NewTextMessage("2348012345678", strings.Repeat("Ọ", 4097)), NewLocation("2348012345678", math.NaN(), 0, "", ""), NewLocation("2348012345678", 91, 0, "", ""), {To: "2348012345678", Type: MessageTypeVideo}, {To: "2348012345678", Type: MessageTypeImage, Media: &MediaBody{ID: "id", Link: "https://example.com/a"}}, NewTextMessage("2348012345678", string([]byte{0xff})), NewQuickReply("2348012345678", "Choose")}
	for _, m := range invalid {
		if err := m.Validate(); err == nil {
			t.Fatalf("accepted %#v", m)
		}
	}
	mixed := NewTextMessage("2348012345678", "ok")
	mixed.Audio = &AudioBody{ID: "media"}
	if mixed.Validate() == nil {
		t.Fatal("mixed variants accepted")
	}
}
func TestActionTokensAndUnicode(t *testing.T) {
	a := NewQuickReply("2348012345678", "Choose", strings.Repeat("Ọ", 30))
	b := NewQuickReply("2348012345678", "Choose", "Ọlá")
	reply := a.Interactive.Action.Buttons[0].Reply
	if !utf8.ValidString(reply.Title) || utf8.RuneCountInString(reply.Title) != 20 {
		t.Fatal("Unicode corrupted")
	}
	if reply.ID == b.Interactive.Action.Buttons[0].Reply.ID {
		t.Fatal("reused token")
	}
	if _, err := NewQuickReplyActions("2348012345678", "Choose", ButtonReply{ID: "same", Title: "Yes"}, ButtonReply{ID: "same", Title: "No"}); err == nil {
		t.Fatal("duplicate action accepted")
	}
}

func TestInteractiveAndTemplateValidation(t *testing.T) {
	to := "2348012345678"
	valid := []Message{NewListMenu(to, "Choose", "Open", ListSection{Title: "Options", Rows: []ListRow{{ID: "r", Title: "Ọlá"}}}), NewFlowMessage(to, "Header", "Body", "Footer", "flow", "token", "Start", "SCREEN", nil), NewReaction(to, "message", ""), {To: to, Type: MessageTypeImage, Media: &MediaBody{ID: "image"}}}
	for _, m := range valid {
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	cases := []*InteractiveBody{
		nil, {Type: "wrong", Body: InteractiveText{Text: "body"}},
		{Type: "button", Body: InteractiveText{Text: "body"}, Header: &InteractiveText{Type: "image", Text: "head"}},
		{Type: "button", Body: InteractiveText{Text: "body"}, Footer: &InteractiveText{Text: strings.Repeat("x", 61)}},
		{Type: "button", Body: InteractiveText{Text: "body"}, Action: InteractiveAction{Buttons: []InteractiveButton{{Type: "wrong", Reply: ButtonReply{ID: "a", Title: "A"}}}}},
		{Type: "list", Body: InteractiveText{Text: "body"}, Action: InteractiveAction{Button: "open", Sections: []ListSection{{Rows: []ListRow{{ID: "a", Title: "A"}, {ID: "a", Title: "B"}}}}}},
		{Type: "list", Body: InteractiveText{Text: "body"}, Action: InteractiveAction{Button: "open", Sections: []ListSection{{Title: "Empty"}}}},
		{Type: "list", Body: InteractiveText{Text: "body"}},
		{Type: "flow", Body: InteractiveText{Text: "body"}},
		{Type: "flow", Body: InteractiveText{Text: "body"}, Action: InteractiveAction{Name: "flow", Parameters: &FlowParameters{FlowMessageVersion: "wrong"}}},
	}
	for _, i := range cases {
		if validInteractive(i) {
			t.Fatalf("invalid interactive accepted: %#v", i)
		}
	}
	for _, c := range []TemplateComponent{{Type: "wrong"}, {Type: "button", SubType: "wrong", Index: "0"}, {Type: "body", Index: "0"}, {Type: "body", Parameters: []TemplateParameter{{Type: "image"}}}, {Type: "body", Parameters: make([]TemplateParameter, 101)}} {
		if validTemplate(&TemplateBody{Name: "name", Language: TemplateLanguage{Code: "en"}, Components: []TemplateComponent{c}}) {
			t.Fatal("invalid template accepted")
		}
	}
	if validTemplate(nil) {
		t.Fatal("nil template accepted")
	}
}

func TestContactFieldValidation(t *testing.T) {
	to := "2348012345678"
	if err := NewContactCard(to, "Ọlá", "", "", "Studio", "Engineer").Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []ContactCard{{Name: ContactName{}}, {Name: ContactName{FormattedName: "Ọlá"}, Phones: []ContactPhone{{Phone: "invalid"}}}, {Name: ContactName{FormattedName: "Ọlá"}, Emails: []ContactEmail{{Email: "invalid"}}}, {Name: ContactName{FormattedName: "Ọlá"}, Org: &ContactOrg{Title: strings.Repeat("x", 257)}}} {
		if (Message{To: to, Type: MessageTypeContacts, Contacts: []ContactCard{c}}).Validate() == nil {
			t.Fatal("invalid contact accepted")
		}
	}
}
