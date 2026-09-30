package protocol

import (
	"regexp"
	"strings"
)

var legacyCompanionMarker = regexp.MustCompile(`\[(动作|台词)\]`)

// NormalizeCompanionReply repairs legacy bracket-labelled replies emitted
// before the action/speech tag contract existed.
func NormalizeCompanionReply(reply CompanionReply) (CompanionReply, bool) {
	if len(reply.Actions) == 0 && !legacyCompanionMarker.MatchString(reply.Speech) {
		return reply, false
	}
	actions, speech := splitLegacyCompanionText(reply.Speech)
	if len(actions) > 0 {
		reply.Actions = compactCompanionStrings(append(reply.Actions, actions...))
	}
	reply.Speech = strings.TrimSpace(speech)
	if len(reply.Actions) == 0 && reply.Speech == "" {
		reply.Speech = "嗯，我在。"
	}
	return reply, true
}

func splitLegacyCompanionText(text string) ([]string, string) {
	matches := legacyCompanionMarker.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, text
	}
	var actions []string
	var speech []string
	state := ""
	start := 0
	appendPart := func(part string) {
		part = strings.TrimSpace(part)
		if part == "" {
			return
		}
		switch state {
		case "动作":
			actions = append(actions, part)
		case "台词":
			speech = append(speech, part)
		}
	}
	for _, match := range matches {
		appendPart(text[start:match[0]])
		state = text[match[2]:match[3]]
		start = match[1]
	}
	appendPart(text[start:])
	return compactCompanionStrings(actions), strings.Join(speech, "\n")
}

func compactCompanionStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}
