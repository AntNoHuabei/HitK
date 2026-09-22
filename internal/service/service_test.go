package service

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hitk/hitk/internal/config"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/hitk/hitk/internal/provider"
)

// fakeProvider stands in for a cloud vendor so the whole stack can be driven
// without network access.
type fakeProvider struct {
	mu    sync.Mutex
	steps [][]provider.Chunk
	calls int
}

func (f *fakeProvider) ID() string { return "fake" }

func (f *fakeProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.Chunk, error) {
	f.mu.Lock()
	idx := f.calls
	f.calls++
	var script []provider.Chunk
	if idx < len(f.steps) {
		script = f.steps[idx]
	} else {
		script = []provider.Chunk{{Kind: provider.ChunkText, Text: "（无更多脚本）"}, {Kind: provider.ChunkDone}}
	}
	f.mu.Unlock()

	out := make(chan provider.Chunk, len(script))
	for _, ch := range script {
		out <- ch
	}
	close(out)
	return out, nil
}

// collector captures the protocol event stream the app would push to the UI.
type collector struct {
	mu     sync.Mutex
	events []*protocol.Event
}

func (c *collector) emit(ev *protocol.Event) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *collector) has(kind protocol.EventKind) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}

// sawToolCall reports whether any item event described a tool invocation, which
// is what the UI renders as a tool card.
func (c *collector) sawToolCall() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events {
		if ev.Item != nil && ev.Item.Type == protocol.ItemToolCall {
			return true
		}
	}
	return false
}

func (c *collector) waitFor(t *testing.T, what string, pred func([]*protocol.Event) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		snapshot := append([]*protocol.Event(nil), c.events...)
		c.mu.Unlock()
		if pred(snapshot) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// newTestContainer builds the real container against a temporary data
// directory, which is what the app does on launch.
func newTestContainer(t *testing.T, prov provider.Provider) (*Container, *collector, config.Paths) {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{
		DataDir:    dir,
		ConfigFile: filepath.Join(dir, "config.json"),
		KBFile:     filepath.Join(dir, "kb.db"),
		ThreadsDir: filepath.Join(dir, "threads"),
		LogFile:    filepath.Join(dir, "hitk.log"),
	}

	store, err := config.Load(paths)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	events := &collector{}
	container, err := NewContainer(paths, store, events.emit)
	if err != nil {
		t.Fatalf("new container: %v", err)
	}
	t.Cleanup(container.Close)

	if prov != nil {
		container.Providers.Register(prov)
	}
	return container, events, paths
}

// TestKnowledgeBaseRoundTrip covers the knowledge base the agent searches:
// creating a collection, ingesting text, retrieving it and reading it back.
func TestKnowledgeBaseRoundTrip(t *testing.T) {
	container, _, _ := newTestContainer(t, nil)
	kbSvc := NewKBService(container)

	col, err := kbSvc.CreateCollection("产品手册", "测试用知识库")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	body := "退款政策：自签收之日起七日内，商品未拆封可申请无理由退款。" +
		"超过七日的订单仅支持质量问题换货，运费由平台承担。"
	res, err := kbSvc.AddText(col.ID, "退款政策", body)
	if err != nil {
		t.Fatalf("add text: %v", err)
	}
	if res.Added != 1 {
		t.Fatalf("import added %d documents, want 1 (message: %s)", res.Added, res.Message)
	}

	docs, err := kbSvc.Documents(col.ID)
	if err != nil {
		t.Fatalf("documents: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("collection holds %d documents, want 1", len(docs))
	}
	if docs[0].ChunkCount == 0 {
		t.Error("document was ingested without any chunks, so it can never be retrieved")
	}

	hits, err := kbSvc.Search("退款需要几天", col.ID, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("search returned no hits for text that is in the collection")
	}
	if hits[0].DocTitle != "退款政策" {
		t.Errorf("top hit title = %q, want 退款政策", hits[0].DocTitle)
	}
	if !strings.Contains(hits[0].Snippet, "退款") {
		t.Errorf("snippet does not contain the matched text: %q", hits[0].Snippet)
	}

	content, err := kbSvc.DocumentContent(docs[0].ID)
	if err != nil {
		t.Fatalf("document content: %v", err)
	}
	if !strings.Contains(content, "七日") {
		t.Error("document content lost the ingested text")
	}

	stats := kbSvc.Stats()
	if stats.Collections < 1 || stats.Documents < 1 || stats.Chunks < 1 {
		t.Errorf("stats do not reflect the ingested data: %+v", stats)
	}
}

// TestTurnRunsSearchToolEndToEnd drives a full turn through the service layer:
// the model asks for the knowledge Skill, the real tool retrieves from the real index,
// and the final answer is persisted to the thread.
func TestTurnRunsSearchToolEndToEnd(t *testing.T) {
	fake := &fakeProvider{steps: [][]provider.Chunk{
		{
			{Kind: provider.ChunkToolCall, Tool: &provider.ToolCallReq{
				ID:        "call_search",
				Name:      "skill_run",
				Arguments: `{"skill":"knowledge","command":"search","args":["--query","退款政策","--query","退货规则","--limit","3"]}`,
			}},
			{Kind: provider.ChunkUsage, Usage: &provider.Usage{InputTokens: 210, OutputTokens: 24}},
			{Kind: provider.ChunkDone},
		},
		{
			{Kind: provider.ChunkText, Text: "根据《退款政策》，"},
			{Kind: provider.ChunkText, Text: "七日內未拆封可无理由退款。"},
			{Kind: provider.ChunkUsage, Usage: &provider.Usage{InputTokens: 480, OutputTokens: 30}},
			{Kind: provider.ChunkDone},
		},
	}}

	container, events, _ := newTestContainer(t, fake)

	kbSvc := NewKBService(container)
	col, err := kbSvc.CreateCollection("售后", "")
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if _, err := kbSvc.AddText(col.ID, "退款政策",
		"退款政策：自签收之日起七日内，商品未拆封可申请无理由退款。超过七日仅支持质量问题换货。"); err != nil {
		t.Fatalf("add text: %v", err)
	}

	chatSvc := NewChatService(container)
	thread, err := chatSvc.CreateThread("work")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	if err := chatSvc.SubmitTurn(protocol.Op{
		Kind:       protocol.OpTurnStart,
		ThreadID:   thread.ID,
		Mode:       protocol.ModeWork,
		Provider:   "fake",
		Model:      "fake-1",
		Collection: col.ID,
		Policy:     protocol.ApprovalAuto,
		Input:      []protocol.InputPart{{Type: "text", Text: "退款要几天？"}},
	}); err != nil {
		t.Fatalf("submit turn: %v", err)
	}

	events.waitFor(t, "turn to complete", func(list []*protocol.Event) bool {
		for _, ev := range list {
			if ev.Kind == protocol.EvTurnCompleted || ev.Kind == protocol.EvTurnAborted {
				return true
			}
		}
		return false
	})

	if !events.has(protocol.EvTurnCompleted) {
		t.Fatal("turn did not complete")
	}
	if !events.sawToolCall() {
		t.Fatal("no tool call was reported to the UI")
	}

	// Reload from disk so the assertions cover what a reopened app would show.
	stored, err := chatSvc.GetThread(thread.ID)
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}

	var toolItem *protocol.Item
	var answer string
	var refs int
	for _, it := range stored.Items {
		switch it.Type {
		case protocol.ItemToolCall:
			toolItem = it
		case protocol.ItemAgentMessage:
			if strings.TrimSpace(it.Text) != "" {
				answer = it.Text
			}
			refs += len(it.Refs)
		}
	}

	if toolItem == nil {
		t.Fatal("the tool call was not persisted to the thread")
	}
	if toolItem.Status != protocol.StatusCompleted {
		t.Errorf("tool call status = %q, want completed (result: %s)", toolItem.Status, toolItem.Tool.Result)
	}
	if toolItem.Tool == nil || !strings.Contains(toolItem.Tool.Result, "退款") {
		t.Errorf("knowledge search did not return the ingested text: %+v", toolItem.Tool)
	}
	if answer == "" {
		t.Fatal("the model's answer was not persisted")
	}
	if answer != "根据《退款政策》，七日內未拆封可无理由退款。" {
		t.Errorf("streamed fragments were not assembled in order: %q", answer)
	}
	// Retrieved snippets must reach the UI as citations, otherwise Agentic
	// Search is indistinguishable from a model inventing an answer.
	if refs == 0 {
		t.Error("the answer carries no citations from the knowledge base")
	}

	if fake.calls != 2 {
		t.Errorf("model was called %d times, want 2 (search then answer)", fake.calls)
	}
}

// TestSecondTurnOnSameThreadIsRejected mirrors what the UI relies on: a thread
// runs one turn at a time.
func TestSecondTurnOnSameThreadIsRejected(t *testing.T) {
	container, events, _ := newTestContainer(t, nil)
	chatSvc := NewChatService(container)

	thread, err := chatSvc.CreateThread("work")
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}

	// A provider that never finishes on its own holds the turn open, which is
	// the state a second submission must be rejected in.
	blocking := &blockingProvider{}
	container.Providers.Register(blocking)

	op := protocol.Op{
		Kind:     protocol.OpTurnStart,
		ThreadID: thread.ID,
		Provider: "blocking",
		Model:    "slow",
		Policy:   protocol.ApprovalAuto,
		Input:    []protocol.InputPart{{Type: "text", Text: "长任务"}},
	}
	if err := chatSvc.SubmitTurn(op); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	events.waitFor(t, "turn to start", func(list []*protocol.Event) bool {
		for _, ev := range list {
			if ev.Kind == protocol.EvTurnStarted {
				return true
			}
		}
		return false
	})

	if err := chatSvc.SubmitTurn(op); err == nil {
		t.Fatal("a second turn was accepted while the first was still running")
	}
	if !chatSvc.Running(thread.ID) {
		t.Error("Running reported false while a turn was in flight")
	}

	if err := chatSvc.Interrupt(thread.ID); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	events.waitFor(t, "turn to abort", func(list []*protocol.Event) bool {
		for _, ev := range list {
			if ev.Kind == protocol.EvTurnAborted {
				return true
			}
		}
		return false
	})
	// The abort event is published before the turn goroutine releases the
	// thread, and that goroutine writes the transcript on its way out. Waiting
	// for the thread to go idle keeps the tail from landing in the temp
	// directory after cleanup has already deleted it.
	waitIdle(t, chatSvc, thread.ID)
}

// waitIdle blocks until a thread no longer has a turn in flight.
func waitIdle(t *testing.T, chatSvc *ChatService, threadID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !chatSvc.Running(threadID) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("thread still has a turn in flight")
}

// blockingProvider holds its stream open until the turn is cancelled.
type blockingProvider struct{}

func (b *blockingProvider) ID() string { return "blocking" }

func (b *blockingProvider) Stream(ctx context.Context, _ provider.ChatRequest) (<-chan provider.Chunk, error) {
	out := make(chan provider.Chunk, 1)
	out <- provider.Chunk{Kind: provider.ChunkText, Text: "处理中…"}
	go func() {
		defer close(out)
		<-ctx.Done()
	}()
	return out, nil
}

// TestSettingsSurviveARestart checks that credentials and the model choice are
// written to disk, since losing an API key on every launch would be unusable.
func TestSettingsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{
		DataDir:    dir,
		ConfigFile: filepath.Join(dir, "config.json"),
		KBFile:     filepath.Join(dir, "kb.db"),
		ThreadsDir: filepath.Join(dir, "threads"),
		LogFile:    filepath.Join(dir, "hitk.log"),
	}

	store, err := config.Load(paths)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	container, err := NewContainer(paths, store, func(*protocol.Event) {})
	if err != nil {
		t.Fatalf("new container: %v", err)
	}
	settings := NewSettingsService(container)
	models := NewModelService(container)

	if _, err := models.SaveProvider(ProviderInput{
		CatalogID: "deepseek",
		APIKey:    "sk-test-key",
		Model:     "deepseek-flash",
		Models: []ProviderModelView{
			{Model: "deepseek-flash", Label: "DeepSeek V4.1 Flash"},
			{Model: "deepseek-v4-pro", Label: "DeepSeek V4 Pro"},
		},
	}); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	// Work and Coding must be independently addressable, which is the whole
	// point of splitting the old single provider/model pair.
	if err := models.SetModeDefault("coding", "deepseek", "deepseek-v4-pro"); err != nil {
		t.Fatalf("set mode default: %v", err)
	}
	if err := settings.SaveOptions(9, 64000, "", ""); err != nil {
		t.Fatalf("save options: %v", err)
	}
	container.Close()

	// Reopen exactly as a second launch would.
	reopened, err := config.Load(paths)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	got := reopened.Get()
	work, coding := got.ModeDefaults["work"], got.ModeDefaults["coding"]
	if work.Provider != "deepseek" || work.Model != "deepseek-flash" {
		t.Errorf("work model selection lost: %+v", work)
	}
	if coding.Provider != "deepseek" || coding.Model != "deepseek-v4-pro" {
		t.Errorf("coding model selection lost: %+v", coding)
	}
	if got.MaxSteps != 9 {
		t.Errorf("maxSteps = %d, want 9", got.MaxSteps)
	}
	if got.ContextLimit != 64000 {
		t.Errorf("contextLimit = %d, want 64000", got.ContextLimit)
	}

	var found bool
	for _, v := range got.Vendors {
		if v.ID == "deepseek" {
			found = true
			if v.APIKey != "sk-test-key" {
				t.Error("the stored API key did not round-trip")
			}
			if len(v.EnabledModels) != 2 {
				t.Errorf("enabled models = %d, want 2", len(v.EnabledModels))
			}
			if v.DefaultModel != "deepseek-flash" {
				t.Errorf("provider model = %q, want deepseek-flash", v.DefaultModel)
			}
		}
	}
	if !found {
		t.Error("the vendor configuration was not persisted")
	}
}
