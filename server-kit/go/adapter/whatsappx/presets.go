package whatsappx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewVoiceNote creates a push-to-talk voice message with green waveform playback.
// mediaIDOrURL can be a Meta uploaded media ID or an accessible public URL.
func NewVoiceNote(to, mediaIDOrURL string) Message {
	audio := &AudioBody{Voice: true}
	if strings.HasPrefix(mediaIDOrURL, "http://") || strings.HasPrefix(mediaIDOrURL, "https://") {
		audio.Link = mediaIDOrURL
	} else {
		audio.ID = mediaIDOrURL
	}

	return Message{
		To:    to,
		Type:  MessageTypeAudio,
		Audio: audio,
	}
}

// NewAudioMessage creates a standard audio track message.
func NewAudioMessage(to, mediaIDOrURL string) Message {
	audio := &AudioBody{Voice: false}
	if strings.HasPrefix(mediaIDOrURL, "http://") || strings.HasPrefix(mediaIDOrURL, "https://") {
		audio.Link = mediaIDOrURL
	} else {
		audio.ID = mediaIDOrURL
	}

	return Message{
		To:    to,
		Type:  MessageTypeAudio,
		Audio: audio,
	}
}

// NewDocumentMessage creates a document message (PDF, Excel, Word, text).
func NewDocumentMessage(to, mediaIDOrURL, filename, caption string) Message {
	doc := &DocumentBody{
		Filename: filename,
		Caption:  caption,
	}
	if strings.HasPrefix(mediaIDOrURL, "http://") || strings.HasPrefix(mediaIDOrURL, "https://") {
		doc.Link = mediaIDOrURL
	} else {
		doc.ID = mediaIDOrURL
	}

	return Message{
		To:       to,
		Type:     MessageTypeDocument,
		Document: doc,
	}
}

// NewPDFInvoiceMessage creates a PDF invoice document message.
func NewPDFInvoiceMessage(to, pdfURLOrID, invoiceNumber string) Message {
	filename := fmt.Sprintf("Invoice_%s.pdf", invoiceNumber)
	caption := fmt.Sprintf("Here is your invoice #%s.", invoiceNumber)
	return NewDocumentMessage(to, pdfURLOrID, filename, caption)
}

// NewQuickReply creates an interactive quick-reply message with 1 to 3 buttons.
func NewQuickReply(to, bodyText string, buttons ...string) Message {
	var interactiveButtons []InteractiveButton
	for i, title := range buttons {
		if i >= 3 {
			break // Meta limit: maximum 3 buttons per interactive button message
		}
		truncated := truncateString(title, 20)
		interactiveButtons = append(interactiveButtons, InteractiveButton{
			Type: "reply",
			Reply: ButtonReply{
				ID:    newActionID(),
				Title: truncated,
			},
		})
	}

	return Message{
		To:   to,
		Type: MessageTypeInteractive,
		Interactive: &InteractiveBody{
			Type: "button",
			Body: InteractiveText{Text: bodyText},
			Action: InteractiveAction{
				Buttons: interactiveButtons,
			},
		},
	}
}

// NewConfirmationPrompt creates a two-button confirmation dialog.
func NewConfirmationPrompt(to, headerText, bodyText, confirmTitle, cancelTitle string) Message {
	buttons := []InteractiveButton{
		{
			Type: "reply",
			Reply: ButtonReply{
				ID:    newActionID(),
				Title: truncateString(confirmTitle, 20),
			},
		},
		{
			Type: "reply",
			Reply: ButtonReply{
				ID:    newActionID(),
				Title: truncateString(cancelTitle, 20),
			},
		},
	}

	body := &InteractiveBody{
		Type: "button",
		Body: InteractiveText{Text: bodyText},
		Action: InteractiveAction{
			Buttons: buttons,
		},
	}
	if headerText != "" {
		body.Header = &InteractiveText{Type: "text", Text: headerText}
	}

	return Message{
		To:          to,
		Type:        MessageTypeInteractive,
		Interactive: body,
	}
}

// NewListMenu creates an interactive dropdown list message with grouped options.
func NewListMenu(to, bodyText, buttonLabel string, sections ...ListSection) Message {
	return Message{
		To:   to,
		Type: MessageTypeInteractive,
		Interactive: &InteractiveBody{
			Type: "list",
			Body: InteractiveText{Text: bodyText},
			Action: InteractiveAction{
				Button:   truncateString(buttonLabel, 20),
				Sections: sections,
			},
		},
	}
}

// NewFlowMessage creates a native WhatsApp Flow message (interactive form, quiz, survey).
func NewFlowMessage(to, headerText, bodyText, footerText, flowID, flowToken, ctaText, initialScreen string, initialData map[string]any) Message {
	actionPayload := &FlowActionPayload{
		Screen: initialScreen,
		Data:   initialData,
	}

	body := &InteractiveBody{
		Type: "flow",
		Body: InteractiveText{Text: bodyText},
		Action: InteractiveAction{
			Name: "flow",
			Parameters: &FlowParameters{
				FlowMessageVersion: "3",
				FlowToken:          flowToken,
				FlowID:             flowID,
				FlowCTA:            truncateString(ctaText, 20),
				FlowAction:         "navigate",
				FlowActionPayload:  actionPayload,
			},
		},
	}

	if headerText != "" {
		body.Header = &InteractiveText{Type: "text", Text: headerText}
	}
	if footerText != "" {
		body.Footer = &InteractiveText{Type: "text", Text: footerText}
	}

	return Message{
		To:          to,
		Type:        MessageTypeInteractive,
		Interactive: body,
	}
}

// NewQuizFlow launches a multi-question interactive quiz via WhatsApp Flows.
func NewQuizFlow(to, flowID, flowToken, quizTitle, quizDesc string) Message {
	return NewFlowMessage(
		to,
		quizTitle,
		quizDesc,
		"Tap below to start the quiz",
		flowID,
		flowToken,
		"Start Quiz",
		"START_SCREEN",
		nil,
	)
}

// NewSurveyFlow launches a customer satisfaction or poll survey via WhatsApp Flows.
func NewSurveyFlow(to, flowID, flowToken, surveyTitle, surveyDesc string) Message {
	return NewFlowMessage(
		to,
		surveyTitle,
		surveyDesc,
		"Takes less than 1 minute",
		flowID,
		flowToken,
		"Take Survey",
		"QUESTION_1",
		nil,
	)
}

// NewContactCard creates a contact sharing message.
func NewContactCard(to, formattedName, phone, email, company, title string) Message {
	m := Message{
		To:   to,
		Type: MessageTypeContacts,
		Contacts: []ContactCard{
			{
				Name: ContactName{FormattedName: formattedName, FirstName: formattedName},
				Phones: []ContactPhone{
					{Phone: phone, Type: "WORK"},
				},
				Emails: []ContactEmail{
					{Email: email, Type: "WORK"},
				},
				Org: &ContactOrg{
					Company: company,
					Title:   title,
				},
			},
		},
	}
	if phone == "" {
		m.Contacts[0].Phones = nil
	}
	if email == "" {
		m.Contacts[0].Emails = nil
	}
	return m
}

// NewOTPPreset creates a secure one-time password message using an authentication template.
func NewOTPPreset(to, templateName, langCode, otpCode string) Message {
	return Message{
		To:   to,
		Type: MessageTypeTemplate,
		Template: &TemplateBody{
			Name:     templateName,
			Language: TemplateLanguage{Code: langCode},
			Components: []TemplateComponent{
				{
					Type: "body",
					Parameters: []TemplateParameter{
						{Type: "text", Text: otpCode},
					},
				},
				{
					Type:    "button",
					SubType: "url",
					Index:   "0",
					Parameters: []TemplateParameter{
						{Type: "text", Text: otpCode},
					},
				},
			},
		},
	}
}

// NewOrderStatusPreset creates a transaction status update message.
func NewOrderStatusPreset(to, templateName, langCode, customerName, orderID, statusText string) Message {
	return Message{
		To:   to,
		Type: MessageTypeTemplate,
		Template: &TemplateBody{
			Name:     templateName,
			Language: TemplateLanguage{Code: langCode},
			Components: []TemplateComponent{
				{
					Type: "body",
					Parameters: []TemplateParameter{
						{Type: "text", Text: customerName},
						{Type: "text", Text: orderID},
						{Type: "text", Text: statusText},
					},
				},
			},
		},
	}
}

// NewReaction creates an emoji reaction targeting a specific message.
func NewReaction(to, targetMessageID, emoji string) Message {
	return Message{
		To:   to,
		Type: MessageTypeReaction,
		Reaction: &ReactionBody{
			MessageID: targetMessageID,
			Emoji:     emoji,
		},
	}
}

// NewLocation creates a geographic coordinate message.
func NewLocation(to string, lat, long float64, name, address string) Message {
	return Message{
		To:   to,
		Type: MessageTypeLocation,
		Location: &LocationBody{
			Latitude:  lat,
			Longitude: long,
			Name:      name,
			Address:   address,
		},
	}
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}

// NewQuickReplyActions accepts opaque tokens already bound by the application to
// a tenant, recipient, conversation, action version and expiry. Persist that binding
// before Send; verify it atomically when handling the reply.
func NewQuickReplyActions(to, body string, actions ...ButtonReply) (Message, error) {
	m := Message{To: to, Type: MessageTypeInteractive, Interactive: &InteractiveBody{Type: "button", Body: InteractiveText{Text: body}}}
	for _, a := range actions {
		m.Interactive.Action.Buttons = append(m.Interactive.Action.Buttons, InteractiveButton{Type: "reply", Reply: a})
	}
	return m, m.Validate()
}
func newActionID() string {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(token[:])
}
