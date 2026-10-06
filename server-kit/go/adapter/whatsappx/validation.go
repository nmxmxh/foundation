package whatsappx

import (
	"errors"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"unicode/utf8"
)

var recipientPattern = regexp.MustCompile(`^\+?[0-9]{7,15}$`)
var templateNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
var templateButtonIndexPattern = regexp.MustCompile(`^[0-9]$`)
var errInvalidMessage = errors.New("whatsappx: invalid or unsupported message payload")

func boundedText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && (!required || s != "")
}
func validMedia(id, link string) bool {
	if (id == "") == (link == "") {
		return false
	}
	if id != "" {
		return boundedText(id, 256, true)
	}
	u, err := url.Parse(link)
	return err == nil && len(link) <= 4096 && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// Validate rejects unsupported types and inconsistent payloads before network dispatch.
// Video is deliberately unsupported until a complete product contract is introduced.
func (m Message) Validate() error {
	if !recipientPattern.MatchString(m.To) {
		return errInvalidMessage
	}
	if messagePayloadCount(m) != 1 {
		return errInvalidMessage
	}
	valid := false
	switch m.Type {
	case MessageTypeText:
		valid = m.Text != nil && boundedText(m.Text.Body, 4096, true)
	case MessageTypeImage:
		valid = m.Media != nil && validMedia(m.Media.ID, m.Media.Link) && boundedText(m.Media.Caption, 1024, false)
	case MessageTypeAudio:
		valid = m.Audio != nil && validMedia(m.Audio.ID, m.Audio.Link)
	case MessageTypeDocument:
		valid = m.Document != nil && validMedia(m.Document.ID, m.Document.Link) && boundedText(m.Document.Caption, 1024, false) && boundedText(m.Document.Filename, 240, false)
	case MessageTypeReaction:
		valid = m.Reaction != nil && boundedText(m.Reaction.MessageID, 256, true) && boundedText(m.Reaction.Emoji, 16, false)
	case MessageTypeLocation:
		if m.Location != nil {
			l := m.Location
			valid = !math.IsNaN(l.Latitude) && !math.IsNaN(l.Longitude) && math.Abs(l.Latitude) <= 90 && math.Abs(l.Longitude) <= 180 && boundedText(l.Name, 1000, false) && boundedText(l.Address, 1000, false)
		}
	case MessageTypeTemplate:
		valid = validTemplate(m.Template)
	case MessageTypeInteractive:
		valid = validInteractive(m.Interactive)
	case MessageTypeContacts:
		valid = validContacts(m.Contacts)
	}
	if !valid {
		return errInvalidMessage
	}
	return nil
}
func validTemplate(t *TemplateBody) bool {
	if t == nil || !templateNamePattern.MatchString(t.Name) || !boundedText(t.Language.Code, 20, true) || len(t.Components) > 10 {
		return false
	}
	for _, c := range t.Components {
		if c.Type != "body" && c.Type != "header" && c.Type != "button" {
			return false
		}
		if c.Type == "button" && (c.SubType != "url" || !templateButtonIndexPattern.MatchString(c.Index)) {
			return false
		}
		if c.Type != "button" && (c.SubType != "" || c.Index != "") {
			return false
		}
		if len(c.Parameters) > 100 {
			return false
		}
		for _, p := range c.Parameters {
			if p.Type != "text" || !boundedText(p.Text, 32768, true) {
				return false
			}
		}
	}
	return true
}
func validInteractive(i *InteractiveBody) bool {
	if i == nil || !boundedText(i.Body.Text, 1024, true) {
		return false
	}
	if i.Header != nil && (i.Header.Type != "text" || !boundedText(i.Header.Text, 60, true)) {
		return false
	}
	if i.Footer != nil && !boundedText(i.Footer.Text, 60, true) {
		return false
	}
	switch i.Type {
	case "button":
		return validButtons(i.Action)
	case "list":
		return validList(i.Action)
	case "flow":
		return validFlow(i.Action)
	default:
		return false
	}
}
func validButtons(a InteractiveAction) bool {
	if a.Parameters != nil || len(a.Sections) > 0 || a.Name != "" || a.Button != "" || len(a.Buttons) < 1 || len(a.Buttons) > 3 {
		return false
	}
	ids := map[string]bool{}
	for _, b := range a.Buttons {
		if b.Type != "reply" || !boundedText(b.Reply.ID, 256, true) || !boundedText(b.Reply.Title, 20, true) || ids[b.Reply.ID] {
			return false
		}
		ids[b.Reply.ID] = true
	}
	return true
}
func validList(a InteractiveAction) bool {
	if a.Parameters != nil || len(a.Buttons) > 0 || a.Name != "" || !boundedText(a.Button, 20, true) || len(a.Sections) < 1 || len(a.Sections) > 10 {
		return false
	}
	total := 0
	ids := map[string]bool{}
	for _, s := range a.Sections {
		if !boundedText(s.Title, 24, false) || len(s.Rows) == 0 {
			return false
		}
		for _, r := range s.Rows {
			total++
			if !validListRow(r) || ids[r.ID] {
				return false
			}
			ids[r.ID] = true
		}
	}
	return total <= 10
}
func validListRow(r ListRow) bool {
	return boundedText(r.ID, 200, true) && boundedText(r.Title, 24, true) && boundedText(r.Description, 72, false)
}
func validFlow(a InteractiveAction) bool {
	p := a.Parameters
	if p == nil || a.Name != "flow" || len(a.Buttons) > 0 || len(a.Sections) > 0 || a.Button != "" {
		return false
	}
	if p.FlowMessageVersion != "3" || !boundedText(p.FlowToken, 256, true) || !boundedText(p.FlowID, 256, true) || !boundedText(p.FlowCTA, 20, true) {
		return false
	}
	return p.FlowAction == "navigate" && p.FlowActionPayload != nil && boundedText(p.FlowActionPayload.Screen, 256, true)
}
func messagePayloadCount(m Message) int {
	count := 0
	for _, present := range []bool{m.Text != nil, m.Template != nil, m.Media != nil, m.Audio != nil, m.Document != nil, m.Interactive != nil, m.Reaction != nil, m.Location != nil, len(m.Contacts) > 0} {
		if present {
			count++
		}
	}
	return count
}
func validContacts(contacts []ContactCard) bool {
	if len(contacts) == 0 || len(contacts) > 20 {
		return false
	}
	for _, c := range contacts {
		if !validContactName(c.Name) || len(c.Phones) > 20 || len(c.Emails) > 20 {
			return false
		}
		if c.Org != nil && !validContactOrg(*c.Org) {
			return false
		}
		for _, p := range c.Phones {
			if !validContactPhone(p) {
				return false
			}
		}
		for _, e := range c.Emails {
			if !validContactEmail(e) {
				return false
			}
		}
	}
	return true
}
func validContactName(n ContactName) bool {
	return boundedText(n.FormattedName, 256, true) && boundedText(n.FirstName, 256, false) && boundedText(n.LastName, 256, false)
}
func validContactOrg(o ContactOrg) bool {
	return boundedText(o.Company, 256, false) && boundedText(o.Department, 256, false) && boundedText(o.Title, 256, false)
}
func validContactPhone(p ContactPhone) bool {
	return recipientPattern.MatchString(p.Phone) && (p.Type == "" || p.Type == "CELL" || p.Type == "WORK" || p.Type == "MAIN" || p.Type == "HOME")
}
func validContactEmail(e ContactEmail) bool {
	_, err := mail.ParseAddress(e.Email)
	return err == nil && boundedText(e.Email, 254, true) && (e.Type == "" || e.Type == "WORK" || e.Type == "HOME")
}
