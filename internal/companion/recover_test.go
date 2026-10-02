package companion

import (
	"path/filepath"
	"testing"
	"time"
)

func recoverManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(filepath.Join(t.TempDir(), "companion.db"))
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestCrashReplaysUserTurnThatNeverAnswered(t *testing.T) {
	manager := recoverManager(t)
	now := time.Now()
	task := TurnTask{
		ID: "task_user_crash", ThreadID: "th_companion", Trigger: TriggerUser,
		Status: TaskGenerating, RequestedAt: now.Add(-time.Minute).UnixMilli(),
	}
	if err := manager.store.SaveTurnTask(task); err != nil {
		t.Fatalf("save: %v", err)
	}

	replayed, ok := manager.replayAfterCrash(task, now)
	if !ok {
		t.Fatal("a user turn whose reply never landed should replay")
	}
	if replayed.Status != TaskWaiting {
		t.Fatalf("status = %s, want waiting", replayed.Status)
	}
	if replayed.Recoveries != 1 {
		t.Fatalf("recoveries = %d, want 1", replayed.Recoveries)
	}
	stored, err := manager.store.TurnTask(task.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Status != TaskWaiting || stored.Recoveries != 1 {
		t.Fatalf("stored = %s/%d, want waiting/1", stored.Status, stored.Recoveries)
	}
}

func TestCrashRetiresStaleProactiveImpulse(t *testing.T) {
	// A proactive impulse that died in the crash has already passed its moment.
	// Reviving it would push a "just saw this" message about nothing.
	manager := recoverManager(t)
	now := time.Now()
	task := TurnTask{
		ID: "task_proactive_crash", ThreadID: "th_companion", Trigger: TriggerProactive,
		Status: TaskGenerating, RequestedAt: now.Add(-time.Minute).UnixMilli(),
	}
	if _, ok := manager.replayAfterCrash(task, now); ok {
		t.Fatal("a dead proactive impulse must not come back")
	}
}

func TestCrashReplaysOnlyPendingDirectorEdits(t *testing.T) {
	manager := recoverManager(t)
	now := time.Now()

	pendingID := newDirectiveID()
	if err := manager.store.SaveDirective(manager.ID(), DirectorDirective{
		ID: pendingID, CharacterID: manager.ID(), Text: "突然下起大雨",
		Status: DirectivePending, CreatedAt: now.UnixMilli(),
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	pendingTask := TurnTask{
		ID: "task_dir_pending", ThreadID: "th_companion", Trigger: TriggerDirector,
		Status: TaskGenerating, DirectiveID: pendingID,
		RequestedAt: now.Add(-time.Minute).UnixMilli(),
	}
	if _, ok := manager.replayAfterCrash(pendingTask, now); !ok {
		t.Fatal("a director edit still pending in the ledger should replay and settle")
	}

	settledID := newDirectiveID()
	if err := manager.store.SaveDirective(manager.ID(), DirectorDirective{
		ID: settledID, CharacterID: manager.ID(), Text: "她妈明天来看她",
		Status: DirectiveApplied, CreatedAt: now.UnixMilli(),
	}); err != nil {
		t.Fatalf("save applied: %v", err)
	}
	settledTask := TurnTask{
		ID: "task_dir_settled", ThreadID: "th_companion", Trigger: TriggerDirector,
		Status: TaskGenerating, DirectiveID: settledID,
		RequestedAt: now.Add(-time.Minute).UnixMilli(),
	}
	if _, ok := manager.replayAfterCrash(settledTask, now); ok {
		t.Fatal("an already-settled director edit must never run a second time")
	}
}

func TestReplayIsBoundedSoACrashLoopCannotDuplicateAnswers(t *testing.T) {
	manager := recoverManager(t)
	now := time.Now()
	task := TurnTask{
		ID: "task_capped", ThreadID: "th_companion", Trigger: TriggerUser,
		Status: TaskGenerating, Recoveries: maxTurnRecoveries,
		RequestedAt: now.Add(-time.Minute).UnixMilli(),
	}
	if _, ok := manager.replayAfterCrash(task, now); ok {
		t.Fatalf("replay must stop at %d, otherwise a crash loop emits the same answer forever", maxTurnRecoveries)
	}
}

func TestOldQuestionIsNotRevivedDaysLater(t *testing.T) {
	manager := recoverManager(t)
	now := time.Now()
	task := TurnTask{
		ID: "task_ancient", ThreadID: "th_companion", Trigger: TriggerUser,
		Status: TaskGenerating,
		RequestedAt: now.Add(-replayWindow - time.Hour).UnixMilli(),
	}
	if _, ok := manager.replayAfterCrash(task, now); ok {
		t.Fatal("a question from outside the replay window should stay retired")
	}
}
