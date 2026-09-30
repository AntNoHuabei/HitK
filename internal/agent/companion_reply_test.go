package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hitk/hitk/internal/protocol"
)

func TestCompanionReplyParserSeparatesActionsAndSpeech(t *testing.T) {
	parser := newCompanionReplyParser()
	chunks := []string{
		"<act", "ion>她抬起手，轻轻",
		"碰了碰你的脸</action>\n<speech>你回来啦。</speech>",
	}
	for _, chunk := range chunks {
		_ = parser.Write(chunk)
	}
	got := parser.Finish()
	want := protocol.CompanionReply{
		Actions: []string{"她抬起手，轻轻碰了碰你的脸"},
		Speech:  "你回来啦。",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reply = %#v, want %#v", got, want)
	}
}

func TestCompanionReplyParserSupportsActionOnly(t *testing.T) {
	parser := newCompanionReplyParser()
	_ = parser.Write("<action>她沉默地抱住你。</action>")
	got := parser.Finish()
	if len(got.Actions) != 1 || got.Actions[0] != "她沉默地抱住你。" || got.Speech != "" {
		t.Fatalf("reply = %#v", got)
	}
}

func TestCompanionReplyParserSupportsSpeechOnly(t *testing.T) {
	parser := newCompanionReplyParser()
	_ = parser.Write("<speech>我在，慢慢说。</speech>")
	got := parser.Finish()
	if len(got.Actions) != 0 || got.Speech != "我在，慢慢说。" {
		t.Fatalf("reply = %#v", got)
	}
}

func TestCompanionReplyParserCollectsMultipleActions(t *testing.T) {
	parser := newCompanionReplyParser()
	_ = parser.Write("<action>她转身。</action><action>她把灯调暗。</action><speech>过来吧。</speech>")
	got := parser.Finish()
	want := []string{"她转身。", "她把灯调暗。"}
	if !reflect.DeepEqual(got.Actions, want) || got.Speech != "过来吧。" {
		t.Fatalf("reply = %#v", got)
	}
}

func TestCompanionReplyParserNeverReturnsBothEmpty(t *testing.T) {
	parser := newCompanionReplyParser()
	got := parser.Finish()
	if len(got.Actions) == 0 && got.Speech == "" {
		t.Fatal("parser returned an empty reply")
	}
}

func TestCompanionReplyParserKeepsUntaggedTextAsSpeech(t *testing.T) {
	parser := newCompanionReplyParser()
	_ = parser.Write("我在听。")
	got := parser.Finish()
	if got.Speech != "我在听。" || len(got.Actions) != 0 {
		t.Fatalf("reply = %#v", got)
	}
}

func TestCompanionReplyParserNormalizesLegacyBracketOutput(t *testing.T) {
	parser := newCompanionReplyParser()
	_ = parser.Write("[台词] 先说一句。\n[动作] 她向前一步。\n[动作] 她轻轻抱住你。\n[台词] 别怕。")
	got := parser.Finish()
	if len(got.Actions) != 2 {
		t.Fatalf("actions = %#v", got.Actions)
	}
	if got.Speech != "先说一句。\n别怕。" {
		t.Fatalf("speech = %q", got.Speech)
	}
}

func TestCompanionReplyParserRecoversFromMismatchedTags(t *testing.T) {
	parser := newCompanionReplyParser()
	_ = parser.Write(
		"<speech>你……喂。</action>" +
			"<action>嘴上骂着，腰却没躲。</action>" +
			"<speech>唔……慢、慢点……</action>" +
			"<action>我侧过头，脸颊绯红。</action>" +
			"<speech>别以为这样就算你赢。</speech>",
	)
	got := parser.Finish()
	wantActions := []string{"嘴上骂着，腰却没躲。", "我侧过头，脸颊绯红。"}
	if !reflect.DeepEqual(got.Actions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", got.Actions, wantActions)
	}
	if strings.Contains(got.Speech, "<action>") || strings.Contains(got.Speech, "</action>") {
		t.Fatalf("speech retained tags: %q", got.Speech)
	}
	if strings.Count(got.Speech, "你……喂。") != 1 || strings.Count(got.Speech, "别以为这样就算你赢。") != 1 {
		t.Fatalf("speech = %q", got.Speech)
	}
}
