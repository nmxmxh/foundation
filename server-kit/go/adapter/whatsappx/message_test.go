package whatsappx

import (
	"encoding/json"
	"testing"
)

func TestMessageBuilders(t *testing.T) {
	textMsg := NewTextMessage("+15551234567", "Hello world")
	if textMsg.Type != MessageTypeText {
		t.Fatalf("expected text type, got %s", textMsg.Type)
	}
	if textMsg.Text == nil || textMsg.Text.Body != "Hello world" {
		t.Fatal("unexpected text body")
	}

	tplMsg := NewTemplateMessage("+15551234567", "shipping_alert", "en_US", "John", "TRACK123")
	if tplMsg.Type != MessageTypeTemplate {
		t.Fatalf("expected template type, got %s", tplMsg.Type)
	}
	if tplMsg.Template.Name != "shipping_alert" {
		t.Fatalf("expected template name shipping_alert, got %s", tplMsg.Template.Name)
	}
	if len(tplMsg.Template.Components) != 1 || len(tplMsg.Template.Components[0].Parameters) != 2 {
		t.Fatalf("unexpected template components: %+v", tplMsg.Template.Components)
	}

	data, err := json.Marshal(tplMsg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty json output")
	}
}

func TestPresetsAndVoiceTools(t *testing.T) {
	to := "+15550001111"

	// 1. Voice Note (PTT)
	voiceMsg := NewVoiceNote(to, "media-voice-id")
	if voiceMsg.Type != MessageTypeAudio || voiceMsg.Audio == nil || !voiceMsg.Audio.Voice || voiceMsg.Audio.ID != "media-voice-id" {
		t.Fatalf("unexpected voice note: %+v", voiceMsg.Audio)
	}

	// 2. Audio URL
	audioMsg := NewAudioMessage(to, "https://cdn.example.com/audio.mp3")
	if audioMsg.Audio == nil || audioMsg.Audio.Voice || audioMsg.Audio.Link != "https://cdn.example.com/audio.mp3" {
		t.Fatalf("unexpected audio message: %+v", audioMsg.Audio)
	}

	// 3. Quick Reply Buttons (max 3)
	qrMsg := NewQuickReply(to, "Please choose an option:", "Option 1", "Option 2", "Option 3", "Ignored 4")
	if qrMsg.Type != MessageTypeInteractive || len(qrMsg.Interactive.Action.Buttons) != 3 {
		t.Fatalf("expected 3 quick reply buttons, got %d", len(qrMsg.Interactive.Action.Buttons))
	}
	if qrMsg.Interactive.Action.Buttons[0].Reply.Title != "Option 1" {
		t.Fatalf("unexpected button title: %s", qrMsg.Interactive.Action.Buttons[0].Reply.Title)
	}

	// 4. Confirmation Prompt
	confirmMsg := NewConfirmationPrompt(to, "Warning", "Confirm subscription?", "Confirm", "Cancel")
	if confirmMsg.Interactive == nil || len(confirmMsg.Interactive.Action.Buttons) != 2 {
		t.Fatal("expected 2 confirmation buttons")
	}
	if confirmMsg.Interactive.Header.Text != "Warning" {
		t.Fatalf("unexpected header text: %s", confirmMsg.Interactive.Header.Text)
	}

	// 5. List Menu
	listMsg := NewListMenu(to, "Select your appointment time:", "View Times", ListSection{
		Title: "Morning",
		Rows: []ListRow{
			{ID: "slot_9am", Title: "9:00 AM", Description: "Available"},
			{ID: "slot_10am", Title: "10:00 AM", Description: "Available"},
		},
	})
	if listMsg.Interactive == nil || listMsg.Interactive.Type != "list" {
		t.Fatal("expected list interactive type")
	}
	if len(listMsg.Interactive.Action.Sections) != 1 || len(listMsg.Interactive.Action.Sections[0].Rows) != 2 {
		t.Fatal("unexpected section rows count")
	}

	// 6. OTP Preset
	otpMsg := NewOTPPreset(to, "auth_otp_code", "en_US", "839201")
	if otpMsg.Template == nil || len(otpMsg.Template.Components) != 2 {
		t.Fatal("expected 2 components in OTP preset")
	}

	// 7. Order Status Preset
	orderMsg := NewOrderStatusPreset(to, "order_update", "en_US", "Sarah", "ORD-99", "Delivered")
	if orderMsg.Template == nil || len(orderMsg.Template.Components[0].Parameters) != 3 {
		t.Fatal("expected 3 parameters in order status preset")
	}

	// 8. Reaction
	reactionMsg := NewReaction(to, "wamid.target123", "🔥")
	if reactionMsg.Type != MessageTypeReaction || reactionMsg.Reaction.Emoji != "🔥" {
		t.Fatal("unexpected reaction message")
	}

	// 9. Location
	locMsg := NewLocation(to, 37.7749, -122.4194, "San Francisco HQ", "123 Market St")
	if locMsg.Type != MessageTypeLocation || locMsg.Location.Latitude != 37.7749 {
		t.Fatal("unexpected location message")
	}

	// 10. Document & PDF Invoice
	docMsg := NewDocumentMessage(to, "https://cdn.example.com/spec.pdf", "spec.pdf", "Foundation Architecture Spec")
	if docMsg.Type != MessageTypeDocument || docMsg.Document.Link != "https://cdn.example.com/spec.pdf" || docMsg.Document.Filename != "spec.pdf" {
		t.Fatalf("unexpected document message: %+v", docMsg.Document)
	}

	invoiceMsg := NewPDFInvoiceMessage(to, "https://cdn.example.com/invoices/inv_998.pdf", "INV-998")
	if invoiceMsg.Type != MessageTypeDocument || invoiceMsg.Document.Filename != "Invoice_INV-998.pdf" {
		t.Fatalf("unexpected invoice message: %+v", invoiceMsg.Document)
	}

	// 11. WhatsApp Flows & Quizzes
	flowMsg := NewFlowMessage(to, "Quiz Time", "Take our 2-minute architectural trivia quiz!", "Footer", "flow_12345", "token_abc", "Start Quiz", "QUESTION_1", map[string]any{"user_id": 42})
	if flowMsg.Type != MessageTypeInteractive || flowMsg.Interactive.Type != "flow" {
		t.Fatalf("unexpected flow interactive type: %+v", flowMsg.Interactive)
	}
	if flowMsg.Interactive.Action.Parameters.FlowID != "flow_12345" || flowMsg.Interactive.Action.Parameters.FlowCTA != "Start Quiz" {
		t.Fatalf("unexpected flow parameters: %+v", flowMsg.Interactive.Action.Parameters)
	}

	quizMsg := NewQuizFlow(to, "flow_quiz_99", "quiz_session_1", "Foundation Quiz", "Test your Go & distributed systems knowledge")
	if quizMsg.Interactive.Action.Parameters.FlowActionPayload.Screen != "START_SCREEN" {
		t.Fatalf("unexpected initial screen in quiz flow: %s", quizMsg.Interactive.Action.Parameters.FlowActionPayload.Screen)
	}

	surveyMsg := NewSurveyFlow(to, "flow_survey_88", "survey_session_2", "NPS Survey", "How likely are you to recommend us?")
	if surveyMsg.Interactive.Action.Parameters.FlowCTA != "Take Survey" {
		t.Fatalf("unexpected survey CTA: %s", surveyMsg.Interactive.Action.Parameters.FlowCTA)
	}

	// 12. Contact Card (vCard)
	contactMsg := NewContactCard(to, "Jane Doe", "+15559876543", "jane@example.com", "Ovasabi Studios", "Support Lead")
	if contactMsg.Type != MessageTypeContacts || len(contactMsg.Contacts) != 1 {
		t.Fatalf("unexpected contacts message: %+v", contactMsg)
	}
	if contactMsg.Contacts[0].Name.FormattedName != "Jane Doe" || contactMsg.Contacts[0].Org.Company != "Ovasabi Studios" {
		t.Fatalf("unexpected contact card contents: %+v", contactMsg.Contacts[0])
	}
}

func BenchmarkMarshalFlowMessage(b *testing.B) {
	to := "+15550001111"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := NewQuizFlow(to, "flow_12345", "token_abc", "System Quiz", "Answer questions")
		_, err := json.Marshal(msg)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifySignature(b *testing.B) {
	secret := "secret-key"
	payload := []byte(`{"object":"whatsapp_business_account","entry":[{"id":"123","changes":[]}]}`)
	sig := "sha256=2b1e2e4e"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = VerifySignature(payload, sig, secret)
	}
}
