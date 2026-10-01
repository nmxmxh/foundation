package whatsappx

// MessageType indicates the kind of WhatsApp message.
type MessageType string

const (
	MessageTypeText        MessageType = "text"
	MessageTypeTemplate    MessageType = "template"
	MessageTypeImage       MessageType = "image"
	MessageTypeDocument    MessageType = "document"
	MessageTypeVideo       MessageType = "video"
	MessageTypeAudio       MessageType = "audio"
	MessageTypeInteractive MessageType = "interactive"
	MessageTypeReaction    MessageType = "reaction"
	MessageTypeLocation    MessageType = "location"
	MessageTypeContacts    MessageType = "contacts"
)

// Message represents an outbound WhatsApp Cloud API message.
type Message struct {
	To          string           `json:"to"`
	Type        MessageType      `json:"type"`
	Text        *TextBody        `json:"text,omitempty"`
	Template    *TemplateBody    `json:"template,omitempty"`
	Media       *MediaBody       `json:"image,omitempty"`
	Audio       *AudioBody       `json:"audio,omitempty"`
	Document    *DocumentBody    `json:"document,omitempty"`
	Interactive *InteractiveBody `json:"interactive,omitempty"`
	Reaction    *ReactionBody    `json:"reaction,omitempty"`
	Location    *LocationBody    `json:"location,omitempty"`
	Contacts    []ContactCard    `json:"contacts,omitempty"`
}

// TextBody holds plain text message payload.
type TextBody struct {
	Body       string `json:"body"`
	PreviewURL bool   `json:"preview_url,omitempty"`
}

// TemplateBody references a Meta pre-approved WhatsApp template.
type TemplateBody struct {
	Name       string              `json:"name"`
	Language   TemplateLanguage    `json:"language"`
	Components []TemplateComponent `json:"components,omitempty"`
}

// TemplateLanguage defines the language code for the template.
type TemplateLanguage struct {
	Code string `json:"code"`
}

// TemplateComponent holds component parameters for template variable substitution.
type TemplateComponent struct {
	Type       string              `json:"type"`
	SubType    string              `json:"sub_type,omitempty"`
	Index      string              `json:"index,omitempty"`
	Parameters []TemplateParameter `json:"parameters,omitempty"`
}

// TemplateParameter holds a parameter value.
type TemplateParameter struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MediaBody holds metadata or links for media messages.
type MediaBody struct {
	ID      string `json:"id,omitempty"`
	Link    string `json:"link,omitempty"`
	Caption string `json:"caption,omitempty"`
}

// DocumentBody holds PDF, spreadsheet, or doc metadata and filenames.
type DocumentBody struct {
	ID       string `json:"id,omitempty"`
	Link     string `json:"link,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// AudioBody holds metadata or links for audio and voice notes.
type AudioBody struct {
	ID    string `json:"id,omitempty"`
	Link  string `json:"link,omitempty"`
	Voice bool   `json:"voice,omitempty"` // True indicates a push-to-talk voice note
}

// InteractiveBody defines an interactive message (button, list, flow).
type InteractiveBody struct {
	Type   string            `json:"type"` // "button", "list", "flow"
	Header *InteractiveText  `json:"header,omitempty"`
	Body   InteractiveText   `json:"body"`
	Footer *InteractiveText  `json:"footer,omitempty"`
	Action InteractiveAction `json:"action"`
}

// InteractiveText contains text with optional header format.
type InteractiveText struct {
	Type string `json:"type,omitempty"` // "text"
	Text string `json:"text"`
}

// InteractiveAction contains buttons, list sections, or flow configurations.
type InteractiveAction struct {
	Name       string              `json:"name,omitempty"` // "flow" for WhatsApp Flows
	Parameters *FlowParameters     `json:"parameters,omitempty"`
	Buttons    []InteractiveButton `json:"buttons,omitempty"`
	Button     string              `json:"button,omitempty"` // Label for list menu button
	Sections   []ListSection       `json:"sections,omitempty"`
}

// FlowParameters defines Meta WhatsApp Flow execution parameters (quizzes, forms, surveys).
type FlowParameters struct {
	FlowMessageVersion string             `json:"flow_message_version"` // "3"
	FlowToken          string             `json:"flow_token"`           // Tracking identifier
	FlowID             string             `json:"flow_id"`              // Meta Flow ID
	FlowCTA            string             `json:"flow_cta"`             // Button text e.g. "Start Quiz"
	FlowAction         string             `json:"flow_action"`          // "navigate" or "data_exchange"
	FlowActionPayload  *FlowActionPayload `json:"flow_action_payload,omitempty"`
}

// FlowActionPayload points to the initial screen and data context.
type FlowActionPayload struct {
	Screen string         `json:"screen"`
	Data   map[string]any `json:"data,omitempty"`
}

// InteractiveButton represents a quick-reply action button.
type InteractiveButton struct {
	Type  string      `json:"type"` // "reply"
	Reply ButtonReply `json:"reply"`
}

// ButtonReply contains the button identifier and displayed title.
type ButtonReply struct {
	ID    string `json:"id"`
	Title string `json:"title"` // Max 20 characters per Meta limit
}

// ListSection groups selectable rows in a list menu.
type ListSection struct {
	Title string    `json:"title"`
	Rows  []ListRow `json:"rows"`
}

// ListRow defines a selectable option in an interactive list message.
type ListRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"` // Max 24 characters
	Description string `json:"description,omitempty"`
}

// ReactionBody attaches an emoji reaction to an existing message.
type ReactionBody struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"` // Empty string removes the reaction
}

// LocationBody contains geographic coordinates.
type LocationBody struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Name      string  `json:"name,omitempty"`
	Address   string  `json:"address,omitempty"`
}

// ContactCard represents a vCard entry for sharing business or personnel contacts.
type ContactCard struct {
	Name   ContactName    `json:"name"`
	Phones []ContactPhone `json:"phones,omitempty"`
	Emails []ContactEmail `json:"emails,omitempty"`
	Org    *ContactOrg    `json:"org,omitempty"`
}

type ContactName struct {
	FormattedName string `json:"formatted_name"`
	FirstName     string `json:"first_name,omitempty"`
	LastName      string `json:"last_name,omitempty"`
}

type ContactPhone struct {
	Phone string `json:"phone"`
	Type  string `json:"type,omitempty"` // "CELL", "WORK", "MAIN"
}

type ContactEmail struct {
	Email string `json:"email"`
	Type  string `json:"type,omitempty"` // "WORK", "HOME"
}

type ContactOrg struct {
	Company    string `json:"company,omitempty"`
	Department string `json:"department,omitempty"`
	Title      string `json:"title,omitempty"`
}

// SendResult contains confirmation details for a sent message.
type SendResult struct {
	MessageID string `json:"message_id"`
	Contact   string `json:"contact"`
}

// NewTextMessage creates a text message.
func NewTextMessage(to, body string) Message {
	return Message{
		To:   to,
		Type: MessageTypeText,
		Text: &TextBody{Body: body},
	}
}

// NewTemplateMessage creates a template message with variable parameters.
func NewTemplateMessage(to, templateName, languageCode string, params ...string) Message {
	var compParams []TemplateParameter
	for _, p := range params {
		compParams = append(compParams, TemplateParameter{Type: "text", Text: p})
	}

	components := []TemplateComponent{}
	if len(compParams) > 0 {
		components = append(components, TemplateComponent{
			Type:       "body",
			Parameters: compParams,
		})
	}

	return Message{
		To:   to,
		Type: MessageTypeTemplate,
		Template: &TemplateBody{
			Name:       templateName,
			Language:   TemplateLanguage{Code: languageCode},
			Components: components,
		},
	}
}
