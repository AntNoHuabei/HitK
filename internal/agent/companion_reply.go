package agent

import (
	"strings"

	"github.com/hitk/hitk/internal/protocol"
)

type companionReplyDelta struct {
	Channel string
	Text    string
}

// companionReplyParser incrementally separates action and speech segments.
//
// Any recognized tag switches the current channel. This intentionally repairs
// malformed sequences such as <speech>...</action><action>...</action>, which
// otherwise swallow all later actions into speech.
type companionReplyParser struct {
	state   string
	pending string
	action  strings.Builder
	speech  strings.Builder
	actions []string
}

func newCompanionReplyParser() *companionReplyParser {
	return &companionReplyParser{state: "outside"}
}

// ParseCompanionReplyText reparses a persisted or model-produced reply using
// the same recovery rules as the streaming parser.
func ParseCompanionReplyText(text string) protocol.CompanionReply {
	parser := newCompanionReplyParser()
	_ = parser.Write(text)
	return parser.Finish()
}

func (p *companionReplyParser) Write(text string) []companionReplyDelta {
	if text == "" {
		return nil
	}
	p.pending += text
	return p.drain(false)
}

func (p *companionReplyParser) Finish() protocol.CompanionReply {
	_ = p.drain(true)
	p.closeCurrent()
	reply := protocol.CompanionReply{
		Actions: compactStrings(p.actions),
		Speech:  strings.TrimSpace(p.speech.String()),
	}
	if len(reply.Actions) == 0 && reply.Speech == "" {
		reply.Speech = "嗯，我在。"
	}
	reply, _ = protocol.NormalizeCompanionReply(reply)
	return reply
}

func (p *companionReplyParser) drain(final bool) []companionReplyDelta {
	var deltas []companionReplyDelta
	for p.pending != "" {
		index, tag, rest := nextReplyTag(p.pending)
		if index < 0 {
			if final {
				deltas = append(deltas, p.emitText(p.pending)...)
				p.pending = ""
				return deltas
			}
			keep := partialTagSuffixLen(p.pending)
			if len(p.pending) > keep {
				deltas = append(deltas, p.emitText(p.pending[:len(p.pending)-keep])...)
				p.pending = p.pending[len(p.pending)-keep:]
			}
			return deltas
		}

		if index > 0 {
			deltas = append(deltas, p.emitText(p.pending[:index])...)
		}
		p.closeCurrent()
		switch tag {
		case "<action>":
			p.state = "action"
		case "<speech>":
			p.state = "speech"
		default:
			p.state = "outside"
		}
		p.pending = rest
	}
	return deltas
}

func (p *companionReplyParser) emitText(text string) []companionReplyDelta {
	if text == "" {
		return nil
	}
	if p.state == "action" {
		p.action.WriteString(text)
		return []companionReplyDelta{{Channel: "action", Text: text}}
	}
	p.speech.WriteString(text)
	return []companionReplyDelta{{Channel: "speech", Text: text}}
}

func (p *companionReplyParser) closeCurrent() {
	if p.state == "action" {
		action := strings.TrimSpace(p.action.String())
		if action != "" {
			p.actions = append(p.actions, action)
		}
		p.action.Reset()
	}
	p.state = "outside"
}

func nextReplyTag(value string) (int, string, string) {
	best := -1
	bestTag := ""
	for _, tag := range []string{"<action>", "</action>", "<speech>", "</speech>"} {
		index := strings.Index(value, tag)
		if index < 0 {
			continue
		}
		if best < 0 || index < best {
			best = index
			bestTag = tag
		}
	}
	if best < 0 {
		return -1, "", value
	}
	return best, bestTag, value[best+len(bestTag):]
}

func partialTagSuffixLen(value string) int {
	max := 0
	for _, tag := range []string{"<action>", "</action>", "<speech>", "</speech>"} {
		if size := partialSuffixLen(value, tag); size > max {
			max = size
		}
	}
	return max
}

func partialSuffixLen(value, tag string) int {
	limit := len(tag) - 1
	if len(value) < limit {
		limit = len(value)
	}
	for size := limit; size > 0; size-- {
		if strings.HasPrefix(tag, value[len(value)-size:]) {
			return size
		}
	}
	return 0
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func formatCompanionReply(reply protocol.CompanionReply) string {
	var b strings.Builder
	for _, action := range reply.Actions {
		if text := strings.TrimSpace(action); text != "" {
			b.WriteString("<action>")
			b.WriteString(text)
			b.WriteString("</action>\n")
		}
	}
	if speech := strings.TrimSpace(reply.Speech); speech != "" {
		b.WriteString("<speech>")
		b.WriteString(speech)
		b.WriteString("</speech>")
	}
	return strings.TrimSpace(b.String())
}

func companionReplyText(reply protocol.CompanionReply) string {
	if speech := strings.TrimSpace(reply.Speech); speech != "" {
		return speech
	}
	if len(reply.Actions) > 0 {
		return strings.TrimSpace(reply.Actions[0])
	}
	return ""
}
