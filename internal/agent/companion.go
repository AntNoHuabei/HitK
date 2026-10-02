package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hitk/hitk/internal/protocol"
)

// CompanionRuntime is the narrow integration boundary between the generic
// agent loop and the companion module.
//
// It is deliberately defined by the consumer. The agent knows how to ask for
// context and hand back a completed turn, while internal/companion remains
// responsible for life state, memories, proactive decisions and persistence.
type CompanionRuntime interface {
	// PrepareCompanionTurn returns the generation context for an already
	// resolved companion task. Alignment and world settlement have already
	// happened before the generic agent starts streaming narration.
	PrepareCompanionTurn(ctx context.Context, threadID, turnID, taskID, userText string) (string, error)
	// CompleteCompanionTurn finalizes a task after the narration is persisted.
	CompleteCompanionTurn(threadID, turnID, taskID, userText, assistantText string)
	// FailCompanionTurn records a generation failure without replaying the
	// already-applied world resolution.
	FailCompanionTurn(threadID, turnID, taskID, message string)
}

// AppendCompanionUserMessage persists a user message for a task that may still
// be waiting for the companion to become available.
func (a *Agent) AppendCompanionUserMessage(threadID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("消息不能为空")
	}
	thread, err := a.sessions.Get(threadID)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	item := &protocol.Item{
		ID:        newItemID(),
		TurnID:    "task_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12],
		ThreadID:  threadID,
		Type:      protocol.ItemUserMessage,
		Status:    protocol.StatusCompleted,
		CreatedAt: now,
		Text:      text,
	}
	thread.Items = append(thread.Items, item)
	if err := a.sessions.Save(thread); err != nil {
		return fmt.Errorf("保存用户消息失败: %w", err)
	}
	a.emitItem(protocol.EvItemCompleted, item)
	return nil
}

// AppendCompanionTurnTask persists the visible alignment artifact.
func (a *Agent) AppendCompanionTurnTask(threadID, taskID, summary string, meta map[string]any) error {
	thread, err := a.sessions.Get(threadID)
	if err != nil {
		return err
	}
	// One card per task. A turn that runs twice must not put two cards on the
	// screen -- the card is the record of that task, not an event per call.
	for _, existing := range thread.Items {
		if existing.TurnID == taskID && existing.Type == protocol.ItemCompanionTurn {
			return nil
		}
	}
	now := time.Now().UnixMilli()
	item := &protocol.Item{
		ID:        newItemID(),
		TurnID:    taskID,
		ThreadID:  threadID,
		Type:      protocol.ItemCompanionTurn,
		Status:    protocol.StatusCompleted,
		CreatedAt: now,
		Text:      summary,
		Meta:      meta,
	}
	thread.Items = append(thread.Items, item)
	if err := a.sessions.Save(thread); err != nil {
		return fmt.Errorf("保存陪伴任务失败: %w", err)
	}
	a.emitItem(protocol.EvItemStarted, item)
	a.emitItem(protocol.EvItemCompleted, item)
	return nil
}

// AppendCompanionMessage writes a proactive companion message into a thread
// using the same Item lifecycle as a streamed response.
func (a *Agent) AppendCompanionMessage(threadID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("主动消息不能为空")
	}
	thread, err := a.sessions.Get(threadID)
	if err != nil {
		return err
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("应用正在退出，无法写入主动消息")
	}
	if _, busy := a.running[threadID]; busy {
		a.mu.Unlock()
		return errors.New("当前会话正在生成，暂不写入主动消息")
	}
	a.mu.Unlock()

	now := time.Now().UnixMilli()
	turnID := "turn_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	item := &protocol.Item{
		ID:        newItemID(),
		TurnID:    turnID,
		ThreadID:  threadID,
		Type:      protocol.ItemAgentMessage,
		Status:    protocol.StatusCompleted,
		CreatedAt: now,
		Text:      text,
		Meta:      map[string]any{"proactive": true},
	}
	thread.Items = append(thread.Items, item)
	if err := a.sessions.Save(thread); err != nil {
		return fmt.Errorf("保存主动消息失败: %w", err)
	}
	a.emitItem(protocol.EvItemStarted, item)
	a.emitItem(protocol.EvItemCompleted, item)
	return nil
}
