package companion

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketMeta     = []byte("meta")
	bucketProfile  = []byte("profile")
	bucketState    = []byte("state")
	bucketEvents   = []byte("events")
	bucketThoughts = []byte("thoughts")
	bucketMemories = []byte("memories")
	bucketEffects  = []byte("effects")
	bucketTraits   = []byte("traits")
	bucketTriggers = []byte("trigger_states")
	bucketTasks    = []byte("tasks")
	// Day plans live under the character id so each world keeps its own calendar.
	bucketPlans = []byte("day_plans")
	// Commitments are the environment layer: extras beyond the profile defaults.
	bucketCommitments = []byte("commitments")
	// Directives are the director's ledger: what was asked, what actually landed,
	// and whether it can be taken back. Audit lives here, not in the transcript.
	bucketDirectives = []byte("directives")
)

const (
	metaPrimaryThread              = "primary_thread"
	metaPaused                     = "paused"
	metaUnread                     = "unread"
	metaInitialized                = "initialized"
	metaSchemaVersion              = "schema_version"
	metaRuleHash                   = "rule_hash"
	metaLegacyKeywordEffectCleanup = "legacy_keyword_effects_cleaned_v1"
	metaAdultPersonaMigration      = "adult_persona_migrated_v1"
)

// Store persists one companion world in its own bbolt database.
type Store struct {
	mu sync.RWMutex
	db *bolt.DB
}

type TickCommit struct {
	ID       string
	State    State
	Event    *LifeEvent
	Events   []LifeEvent
	Thought  *PendingThought
	Memories []Memory
	Effects  []ActiveEffect
	Triggers map[string]TriggerState
	Task     *TurnTask
	// Commitments are plans she just agreed to in conversation. They land in the
	// same transaction as the turn that produced them so a crash cannot leave the
	// promise remembered but the calendar unaware.
	Commitments []Commitment
	// Plan is the day she is living through, carried along so a block starting or
	// drifting lands in the same transaction as the state it caused.
	Plan *DayPlan
	// Directive is the director's ledger entry for this turn, written alongside
	// the rows it created so the record of an intervention cannot drift away
	// from the intervention itself.
	Directive *DirectorDirective
}

// OpenStore opens or creates a companion database.
func OpenStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("companion database path is empty")
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{
			bucketMeta, bucketProfile, bucketState, bucketEvents, bucketThoughts, bucketMemories,
			bucketEffects, bucketTraits, bucketTriggers, bucketTasks, bucketPlans, bucketCommitments,
			bucketDirectives,
		} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}
		return tx.Bucket(bucketMeta).Put([]byte(metaSchemaVersion), []byte("3"))
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Profile returns the saved profile for one character, or the built-in default
// carrying that character's id so a missing row can never masquerade as a
// different identity.
func (s *Store) Profile(id string) (Profile, error) {
	if strings.TrimSpace(id) == "" {
		return Profile{}, fmt.Errorf("companion profile id is empty")
	}
	var profile Profile
	err := s.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketProfile).Get([]byte(id))
		if len(raw) == 0 {
			profile = DefaultProfile(time.Now().UnixMilli())
			profile.ID = id
			return nil
		}
		return json.Unmarshal(raw, &profile)
	})
	if profile.ID == "" {
		profile = DefaultProfile(time.Now().UnixMilli())
		profile.ID = id
	}
	return profile, err
}

// SaveProfile writes the profile.
func (s *Store) SaveProfile(profile Profile) error {
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketProfile), profile.ID, profile)
	})
}

// State returns the current state for one character, creating a sensible first
// state if absent.
func (s *Store) State(id string) (State, error) {
	if strings.TrimSpace(id) == "" {
		return State{}, fmt.Errorf("companion state id is empty")
	}
	var state State
	err := s.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketState).Get([]byte(id))
		if len(raw) == 0 {
			state = DefaultState(time.Now().UnixMilli())
			return nil
		}
		return json.Unmarshal(raw, &state)
	})
	if err == nil && len(state.Metrics) == 0 {
		if migrationErr := s.migrateLegacyState(id, time.Now().UnixMilli()); migrationErr != nil {
			return State{}, migrationErr
		}
		err = s.view(func(tx *bolt.Tx) error {
			raw := tx.Bucket(bucketState).Get([]byte(id))
			return json.Unmarshal(raw, &state)
		})
	}
	if err == nil {
		state.Traits = s.loadTraits()
	}
	state = normalizeState(state, time.Now().UnixMilli())
	return state, err
}

// CommitTick writes all durable effects of one simulation step in one
// transaction.
func (s *Store) CommitTick(commit TickCommit) error {
	characterID := commit.ID
	if strings.TrimSpace(characterID) == "" {
		characterID = DefaultID
	}
	return s.update(func(tx *bolt.Tx) error {
		if err := putState(tx, characterID, commit.State); err != nil {
			return err
		}
		if commit.Plan != nil && commit.Plan.Revision > 0 {
			if err := putJSON(tx.Bucket(bucketPlans), dayPlanKey(characterID, commit.Plan.Date), *commit.Plan); err != nil {
				return err
			}
		}
		if commit.Event != nil {
			if err := putJSON(tx.Bucket(bucketEvents), eventKey(*commit.Event), *commit.Event); err != nil {
				return err
			}
		}
		for _, event := range commit.Events {
			if err := putJSON(tx.Bucket(bucketEvents), eventKey(event), event); err != nil {
				return err
			}
		}
		if commit.Thought != nil {
			if err := putJSON(tx.Bucket(bucketThoughts), commit.Thought.ID, *commit.Thought); err != nil {
				return err
			}
		}
		for _, memory := range commit.Memories {
			if err := putJSON(tx.Bucket(bucketMemories), memory.ID, memory); err != nil {
				return err
			}
		}
		for _, commitment := range commit.Commitments {
			if err := putJSON(tx.Bucket(bucketCommitments), characterID+"|"+commitment.ID, commitment); err != nil {
				return err
			}
		}
		if commit.Directive != nil {
			if err := putJSON(tx.Bucket(bucketDirectives), characterID+"|"+commit.Directive.ID, *commit.Directive); err != nil {
				return err
			}
		}
		if commit.Effects != nil {
			if err := tx.DeleteBucket(bucketEffects); err != nil && err != bolt.ErrBucketNotFound {
				return err
			}
			effectsBucket, err := tx.CreateBucket(bucketEffects)
			if err != nil {
				return err
			}
			for _, effect := range commit.Effects {
				if err := putJSON(effectsBucket, effect.ID, effect); err != nil {
					return err
				}
			}
		}
		if commit.Triggers != nil {
			if err := tx.DeleteBucket(bucketTriggers); err != nil && err != bolt.ErrBucketNotFound {
				return err
			}
			triggersBucket, err := tx.CreateBucket(bucketTriggers)
			if err != nil {
				return err
			}
			for id, trigger := range commit.Triggers {
				if err := putJSON(triggersBucket, id, trigger); err != nil {
					return err
				}
			}
		}
		if commit.Task != nil {
			if err := putJSON(tx.Bucket(bucketTasks), commit.Task.ID, *commit.Task); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveState writes only the state snapshot for one character.
func (s *Store) SaveState(id string, state State) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("companion state id is empty")
	}
	return s.update(func(tx *bolt.Tx) error {
		return putState(tx, id, state)
	})
}

// ActiveEffects returns every persisted timed effect.
func (s *Store) ActiveEffects() ([]ActiveEffect, error) {
	var effects []ActiveEffect
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketEffects).ForEach(func(_, raw []byte) error {
			var effect ActiveEffect
			if err := json.Unmarshal(raw, &effect); err == nil {
				effects = append(effects, effect)
			}
			return nil
		})
	})
	return effects, err
}

// SaveEffects replaces the active effect set in one transaction.
func (s *Store) SaveEffects(effects []ActiveEffect) error {
	return s.update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketEffects); err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		bucket, err := tx.CreateBucket(bucketEffects)
		if err != nil {
			return err
		}
		for _, effect := range effects {
			if err := putJSON(bucket, effect.ID, effect); err != nil {
				return err
			}
		}
		return nil
	})
}

// dayPlanKey keeps each character's calendar separate inside its own file, matching
// how profile and state rows are keyed.
func dayPlanKey(id, date string) string { return id + "|" + date }

// DayPlan returns the saved plan for one character's date. The bool is false when
// that day has never been planned, which is not an error.
func (s *Store) DayPlan(id, date string) (DayPlan, bool, error) {
	var plan DayPlan
	err := s.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketPlans).Get([]byte(dayPlanKey(id, date)))
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &plan); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return DayPlan{}, false, err
	}
	if len(plan.Blocks) == 0 {
		return DayPlan{}, false, nil
	}
	return plan, true, nil
}

// SaveDayPlan writes a plan under its own date.
func (s *Store) SaveDayPlan(id string, plan DayPlan) error {
	if plan.Date == "" {
		return fmt.Errorf("day plan date is empty")
	}
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketPlans), dayPlanKey(id, plan.Date), plan)
	})
}

// PruneDayPlans drops this character's plans dated before beforeDate, so the bucket
// cannot grow without bound. Dates sort lexicographically, so the compare is safe.
func (s *Store) PruneDayPlans(id, beforeDate string) error {
	prefix := id + "|"
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketPlans)
		var stale [][]byte
		err := bucket.ForEach(func(k, _ []byte) error {
			key := string(k)
			if !strings.HasPrefix(key, prefix) {
				return nil
			}
			if strings.Compare(key[len(prefix):len(prefix)+10], beforeDate) < 0 {
				stale = append(stale, append([]byte(nil), k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, key := range stale {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveCommitment stores one commitment beyond the profile defaults.
func (s *Store) SaveCommitment(characterID string, commitment Commitment) error {
	if commitment.ID == "" {
		return fmt.Errorf("commitment id is empty")
	}
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketCommitments), characterID+"|"+commitment.ID, commitment)
	})
}

// Commitments returns this character's stored extras.
func (s *Store) Commitments(characterID string) ([]Commitment, error) {
	prefix := characterID + "|"
	var out []Commitment
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCommitments).ForEach(func(k, raw []byte) error {
			if !strings.HasPrefix(string(k), prefix) {
				return nil
			}
			var commitment Commitment
			if err := json.Unmarshal(raw, &commitment); err != nil {
				return nil
			}
			out = append(out, commitment)
			return nil
		})
	})
	return out, err
}

// DeleteCommitment removes one stored commitment.
func (s *Store) DeleteCommitment(characterID, commitmentID string) error {
	return s.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCommitments).Delete([]byte(characterID + "|" + commitmentID))
	})
}

// SaveDirective inserts or replaces one director ledger entry.
func (s *Store) SaveDirective(characterID string, directive DirectorDirective) error {
	if strings.TrimSpace(directive.ID) == "" {
		return fmt.Errorf("directive id is empty")
	}
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketDirectives), characterID+"|"+directive.ID, directive)
	})
}

// Directive returns one director ledger entry.
func (s *Store) Directive(characterID, id string) (DirectorDirective, error) {
	var directive DirectorDirective
	err := s.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketDirectives).Get([]byte(characterID + "|" + id))
		if len(raw) == 0 {
			return fmt.Errorf("directive %q not found", id)
		}
		return json.Unmarshal(raw, &directive)
	})
	return directive, err
}

// Directives returns this character's interventions, newest first.
func (s *Store) Directives(characterID string, limit int) ([]DirectorDirective, error) {
	prefix := characterID + "|"
	var out []DirectorDirective
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketDirectives).ForEach(func(k, raw []byte) error {
			if !strings.HasPrefix(string(k), prefix) {
				return nil
			}
			var directive DirectorDirective
			if err := json.Unmarshal(raw, &directive); err != nil {
				return nil
			}
			out = append(out, directive)
			return nil
		})
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, err
}

// DeleteEvent removes one life event. Events are keyed by occurrence time plus
// id, so the lookup goes by the id suffix rather than recomputing the key.
func (s *Store) DeleteEvent(eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return nil
	}
	suffix := ":" + eventID
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketEvents)
		var remove [][]byte
		if err := bucket.ForEach(func(key, _ []byte) error {
			if strings.HasSuffix(string(key), suffix) {
				remove = append(remove, append([]byte(nil), key...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range remove {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveTurnTask inserts or replaces one turn task.
func (s *Store) SaveTurnTask(task TurnTask) error {
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketTasks), task.ID, task)
	})
}

// TurnTask returns one persisted task.
func (s *Store) TurnTask(id string) (TurnTask, error) {
	var task TurnTask
	err := s.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketTasks).Get([]byte(id))
		if len(raw) == 0 {
			return fmt.Errorf("turn task %q not found", id)
		}
		return json.Unmarshal(raw, &task)
	})
	return task, err
}

// TurnTasks returns tasks newest first, optionally filtered by thread.
func (s *Store) TurnTasks(threadID string, limit int) ([]TurnTask, error) {
	var tasks []TurnTask
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTasks).ForEach(func(_, raw []byte) error {
			var task TurnTask
			if err := json.Unmarshal(raw, &task); err != nil {
				return nil
			}
			if threadID == "" || task.ThreadID == threadID {
				tasks = append(tasks, task)
			}
			return nil
		})
	})
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].RequestedAt > tasks[j].RequestedAt })
	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, err
}

// ActiveTurnTask returns the current active task for a thread.
func (s *Store) ActiveTurnTask(threadID string) (TurnTask, bool, error) {
	tasks, err := s.TurnTasks(threadID, 20)
	if err != nil {
		return TurnTask{}, false, err
	}
	for _, task := range tasks {
		if task.Active() {
			return task, true, nil
		}
	}
	return TurnTask{}, false, nil
}

// RemoveEffectsBySourcePrefix deletes timed effects created by a retired rule
// family. It is used once when keyword-derived conversation effects are
// replaced by structured semantic outcomes.
func (s *Store) RemoveEffectsBySourcePrefix(prefix string) error {
	if strings.TrimSpace(prefix) == "" {
		return nil
	}
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketEffects)
		var remove [][]byte
		if err := bucket.ForEach(func(key, raw []byte) error {
			var effect ActiveEffect
			if err := json.Unmarshal(raw, &effect); err != nil {
				return nil
			}
			if strings.HasPrefix(effect.SourceRuleID, prefix) {
				remove = append(remove, append([]byte(nil), key...))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, key := range remove {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// TriggerStates returns every persisted threshold state.
func (s *Store) TriggerStates() (map[string]TriggerState, error) {
	out := map[string]TriggerState{}
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTriggers).ForEach(func(key, raw []byte) error {
			var state TriggerState
			if err := json.Unmarshal(raw, &state); err == nil {
				out[string(key)] = state
			}
			return nil
		})
	})
	return out, err
}

// SaveTriggerStates replaces all trigger states.
func (s *Store) SaveTriggerStates(states map[string]TriggerState) error {
	return s.update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(bucketTriggers); err != nil && err != bolt.ErrBucketNotFound {
			return err
		}
		bucket, err := tx.CreateBucket(bucketTriggers)
		if err != nil {
			return err
		}
		for id, state := range states {
			if err := putJSON(bucket, id, state); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecentEvents returns events at or after since, newest first.
func (s *Store) RecentEvents(since int64, limit int) ([]LifeEvent, error) {
	var events []LifeEvent
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketEvents).ForEach(func(_, raw []byte) error {
			var event LifeEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				return nil
			}
			if event.OccurredAt >= since {
				events = append(events, event)
			}
			return nil
		})
	})
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt > events[j].OccurredAt })
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, err
}

// ActiveThoughts returns thoughts she has not yet brought up.
func (s *Store) ActiveThoughts(limit int) ([]PendingThought, error) {
	return s.thoughts("active", limit)
}

func (s *Store) thoughts(status string, limit int) ([]PendingThought, error) {
	var thoughts []PendingThought
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketThoughts).ForEach(func(_, raw []byte) error {
			var thought PendingThought
			if err := json.Unmarshal(raw, &thought); err != nil {
				return nil
			}
			if status == "" || thought.Status == status {
				thoughts = append(thoughts, thought)
			}
			return nil
		})
	})
	sort.Slice(thoughts, func(i, j int) bool {
		if thoughts[i].Priority != thoughts[j].Priority {
			return thoughts[i].Priority > thoughts[j].Priority
		}
		return thoughts[i].CreatedAt > thoughts[j].CreatedAt
	})
	if limit > 0 && len(thoughts) > limit {
		thoughts = thoughts[:limit]
	}
	return thoughts, err
}

// MarkThoughtsShared marks every active thought as already carried into chat.
func (s *Store) MarkThoughtsShared(now int64) error {
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketThoughts)
		return bucket.ForEach(func(key, raw []byte) error {
			var thought PendingThought
			if err := json.Unmarshal(raw, &thought); err != nil || thought.Status != "active" {
				return nil
			}
			thought.Status = "shared"
			thought.SharedAt = now
			return putJSON(bucket, string(key), thought)
		})
	})
}

// Memories returns durable memories, highest importance first.
func (s *Store) Memories(limit int) ([]Memory, error) {
	var memories []Memory
	err := s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMemories).ForEach(func(_, raw []byte) error {
			var memory Memory
			if err := json.Unmarshal(raw, &memory); err != nil {
				return nil
			}
			memory = normalizeMemory(memory)
			memories = append(memories, memory)
			return nil
		})
	})
	sort.Slice(memories, func(i, j int) bool {
		if memories[i].Strength != memories[j].Strength {
			return memories[i].Strength > memories[j].Strength
		}
		if memories[i].Importance != memories[j].Importance {
			return memories[i].Importance > memories[j].Importance
		}
		return memories[i].UpdatedAt > memories[j].UpdatedAt
	})
	if limit > 0 && len(memories) > limit {
		memories = memories[:limit]
	}
	return memories, err
}

// TouchMemories reinforces memories selected for the current context.
func (s *Store) TouchMemories(memories []Memory, now int64) error {
	if len(memories) == 0 {
		return nil
	}
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMemories)
		for _, memory := range memories {
			raw := bucket.Get([]byte(memory.ID))
			if len(raw) == 0 {
				continue
			}
			var stored Memory
			if err := json.Unmarshal(raw, &stored); err != nil {
				continue
			}
			stored = normalizeMemory(stored)
			stored.Strength = math.Min(10, stored.Strength+0.25)
			stored.AccessCount++
			stored.LastAccessedAt = now
			stored.UpdatedAt = now
			if err := putJSON(bucket, stored.ID, stored); err != nil {
				return err
			}
		}
		return nil
	})
}

// DecayMemories applies the forgetting curve. Low-strength memories are
// deleted once their strength reaches zero; important or frequently recalled
// memories decay much more slowly.
func (s *Store) DecayMemories(now int64) error {
	return s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMemories)
		var remove [][]byte
		if err := bucket.ForEach(func(key, raw []byte) error {
			var memory Memory
			if err := json.Unmarshal(raw, &memory); err != nil {
				return nil
			}
			memory = normalizeMemory(memory)
			from := memory.LastDecayedAt
			if from == 0 {
				from = memory.CreatedAt
			}
			days := float64(now-from) / float64(24*time.Hour.Milliseconds())
			if days <= 0 {
				return nil
			}
			rate := 0.12
			switch {
			case memory.Importance >= 8:
				rate = 0.025
			case memory.Importance >= 6:
				rate = 0.055
			case memory.Importance >= 4:
				rate = 0.09
			}
			memory.Strength -= rate * days
			memory.LastDecayedAt = now
			if memory.Strength <= 0 {
				remove = append(remove, append([]byte(nil), key...))
				return nil
			}
			memory.Strength = math.Min(10, memory.Strength)
			return putJSON(bucket, memory.ID, memory)
		}); err != nil {
			return err
		}
		for _, key := range remove {
			if err := bucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}

// SaveThought inserts or replaces one pending thought.
func (s *Store) SaveThought(thought PendingThought) error {
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketThoughts), thought.ID, thought)
	})
}

// SaveMemory inserts or replaces one memory.
func (s *Store) SaveMemory(memory Memory) error {
	memory = normalizeMemory(memory)
	return s.update(func(tx *bolt.Tx) error {
		return putJSON(tx.Bucket(bucketMemories), memory.ID, memory)
	})
}

// DeleteMemory removes one memory.
func (s *Store) DeleteMemory(id string) error {
	return s.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketMemories).Delete([]byte(id))
	})
}

// Meta reads a string metadata value.
func (s *Store) Meta(key string) (string, error) {
	var value string
	err := s.view(func(tx *bolt.Tx) error {
		value = string(tx.Bucket(bucketMeta).Get([]byte(key)))
		return nil
	})
	return value, err
}

// SetMeta writes a string metadata value.
func (s *Store) SetMeta(key, value string) error {
	return s.update(func(tx *bolt.Tx) error {
		if value == "" {
			return tx.Bucket(bucketMeta).Delete([]byte(key))
		}
		return tx.Bucket(bucketMeta).Put([]byte(key), []byte(value))
	})
}

// SetUnread replaces the unread proactive-message count atomically.
func (s *Store) SetUnread(value int) error {
	if value < 0 {
		value = 0
	}
	return s.SetMeta(metaUnread, strconv.Itoa(value))
}

// AddUnread increments the unread count and returns the new value.
func (s *Store) AddUnread(delta int) (int, error) {
	next := 0
	err := s.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketMeta)
		current, _ := strconv.Atoi(string(bucket.Get([]byte(metaUnread))))
		next = current + delta
		if next < 0 {
			next = 0
		}
		return bucket.Put([]byte(metaUnread), []byte(strconv.Itoa(next)))
	})
	return next, err
}

// RecordProactiveSent stamps when a proactive message went out and raises the
// unanswered count, in one transaction. The manager must not read State, tweak
// these two fields, and write it back: a tick landing in between would silently
// swallow the count, and the whole backoff rule depends on that count being
// real. Returns the new unanswered total.
func (s *Store) RecordProactiveSent(characterID string, at int64) (int, error) {
	next := 0
	err := s.update(func(tx *bolt.Tx) error {
		var state State
		raw := tx.Bucket(bucketState).Get([]byte(characterID))
		if len(raw) == 0 {
			state = DefaultState(time.Now().UnixMilli())
		} else if err := json.Unmarshal(raw, &state); err != nil {
			return err
		}
		// putState rewrites the trait bucket from the struct, so traits have to
		// be loaded from this same tx or they would be lost.
		traits := map[TraitID]float64{}
		_ = tx.Bucket(bucketTraits).ForEach(func(key, raw []byte) error {
			var value float64
			if err := json.Unmarshal(raw, &value); err == nil {
				traits[TraitID(string(key))] = value
			}
			return nil
		})
		if len(traits) == 0 {
			traits = defaultTraitValues()
		}
		state.Traits = traits

		next = state.UnrepliedProactive + 1
		if next < 0 {
			next = 0
		}
		state.UnrepliedProactive = next
		state.LastProactiveAt = at
		state.UpdatedAt = at
		return putState(tx, characterID, state)
	})
	return next, err
}

// Reset clears the world while preserving the database itself.
func (s *Store) Reset(profile Profile, state State) error {
	return s.update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{
			bucketEvents, bucketThoughts, bucketMemories, bucketEffects, bucketTraits, bucketTriggers, bucketTasks,
		} {
			if err := tx.DeleteBucket(bucket); err != nil && err != bolt.ErrBucketNotFound {
				return err
			}
			if _, err := tx.CreateBucket(bucket); err != nil {
				return err
			}
		}
		if err := putJSON(tx.Bucket(bucketProfile), profile.ID, profile); err != nil {
			return err
		}
		if err := putState(tx, profile.ID, state); err != nil {
			return err
		}
		meta := tx.Bucket(bucketMeta)
		for _, key := range []string{metaPrimaryThread, metaPaused, metaUnread} {
			if err := meta.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) view(fn func(*bolt.Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return fmt.Errorf("companion store is closed")
	}
	return s.db.View(fn)
}

func (s *Store) update(fn func(*bolt.Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return fmt.Errorf("companion store is closed")
	}
	return s.db.Update(fn)
}

func putJSON(bucket *bolt.Bucket, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(key), raw)
}

func eventKey(event LifeEvent) string {
	return fmt.Sprintf("%020d:%s", event.OccurredAt, event.ID)
}

// DefaultProfile returns the first-run profile.
func DefaultProfile(now int64) Profile {
	return Profile{
		ID:               DefaultID,
		Name:             DefaultName,
		Summary:          DefaultSummary,
		Traits:           []string{"温柔", "直接", "愿意亲密", "尊重明确撤回"},
		ScheduleTendency: "balanced",
		ProactiveEnabled: true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// DefaultState returns the first-run state.
func DefaultState(now int64) State {
	at := time.UnixMilli(now)
	block := templateBlockFor(DefaultProfile(now), at)
	startedAt, endsAt := blockBounds(block, at)
	return State{
		Metrics:           defaultMetricValues(),
		Traits:            defaultTraitValues(),
		CurrentActivity:   block.Label,
		ActivityKind:      block.Kind,
		Consciousness:     ConsciousAwake,
		SceneStage:        "none",
		ConsentState:      "not_applicable",
		ActivityStartedAt: startedAt,
		ActivityEndsAt:    endsAt,
		UpdatedAt:         now,
		LastTickAt:        now,
		LastMemoryDecayAt: now,
	}
}

func normalizeState(state State, now int64) State {
	if len(state.Metrics) == 0 {
		return DefaultState(now)
	}
	defaults := DefaultState(now)
	if state.Metrics == nil {
		state.Metrics = map[MetricID]float64{}
	}
	for id, value := range defaults.Metrics {
		if _, ok := state.Metrics[id]; !ok {
			state.Metrics[id] = value
		}
	}
	if state.Traits == nil {
		state.Traits = map[TraitID]float64{}
	}
	for id, value := range defaults.Traits {
		if _, ok := state.Traits[id]; !ok {
			state.Traits[id] = value
		}
	}
	if state.LastMemoryDecayAt == 0 {
		state.LastMemoryDecayAt = now
	}
	if state.SceneStage == "" {
		state.SceneStage = "none"
	}
	if state.ConsentState == "" {
		state.ConsentState = "not_applicable"
	}
	if (state.SceneStage == "none" || state.SceneStage == "stopped") && state.SceneStartedAt != 0 {
		state.SceneStartedAt = 0
	}
	if state.SceneStage != "none" && state.SceneStage != "stopped" && state.SceneStartedAt == 0 {
		state.SceneStartedAt = state.SceneUpdatedAt
		if state.SceneStartedAt == 0 {
			state.SceneStartedAt = now
		}
	}
	// A pending check-in with no live scene to check in on is a leftover, not a
	// promise. Clearing it here keeps "no check-in without a scene" true on load
	// rather than only true on the paths that remember to maintain it.
	if state.SceneStage == "none" || state.SceneStage == "stopped" {
		state.SceneCheckInAt = 0
	}
	return state
}

func (state State) Metric(id MetricID) float64 {
	return state.Metrics[id]
}

func (state State) Trait(id TraitID) float64 {
	return state.Traits[id]
}

func (state *State) AddMetric(id MetricID, delta float64) {
	if state.Metrics == nil {
		state.Metrics = map[MetricID]float64{}
	}
	state.Metrics[id] += delta
}

func (state *State) AddTrait(id TraitID, delta float64) {
	if state.Traits == nil {
		state.Traits = map[TraitID]float64{}
	}
	state.Traits[id] += delta
}

func defaultMetricValues() map[MetricID]float64 {
	return map[MetricID]float64{
		MetricVitality:           70,
		MetricHunger:             20,
		MetricFatigue:            25,
		MetricSleepPressure:      20,
		MetricAlertness:          75,
		MetricCognitiveLoad:      20,
		MetricMotivation:         60,
		MetricFlow:               10,
		MetricMoodValence:        20,
		MetricStress:             20,
		MetricAnxiety:            18,
		MetricIrritability:       12,
		MetricBoredom:            5,
		MetricLoneliness:         35,
		MetricSocialEnergy:       65,
		MetricLibido:             35,
		MetricSexualSatisfaction: 45,
		MetricMissing:            0,
		MetricTrust:              50,
		MetricIntimacy:           25,
	}
}

func defaultTraitValues() map[TraitID]float64 {
	return map[TraitID]float64{
		TraitResilience:       50,
		TraitFitness:          40,
		TraitSleepDebt:        10,
		TraitSensitivity:      50,
		TraitAttachmentSecure: 45,
		TraitNutritionBalance: 55,
	}
}

type legacyState struct {
	Energy             float64 `json:"energy"`
	Hunger             float64 `json:"hunger"`
	Fatigue            float64 `json:"fatigue"`
	Happiness          float64 `json:"happiness"`
	Calm               float64 `json:"calm"`
	Anxiety            float64 `json:"anxiety"`
	Loneliness         float64 `json:"loneliness"`
	SocialSatisfaction float64 `json:"socialSatisfaction"`
	Libido             float64 `json:"libido"`
	Stress             float64 `json:"stress"`
	MissingYou         float64 `json:"missingYou"`
	Trust              float64 `json:"trust"`
	Intimacy           float64 `json:"intimacy"`
	ActivityKind       string  `json:"activityKind"`
	CurrentActivity    string  `json:"currentActivity"`
	ActivityStartedAt  int64   `json:"activityStartedAt"`
	ActivityEndsAt     int64   `json:"activityEndsAt"`
	UpdatedAt          int64   `json:"updatedAt"`
	LastTickAt         int64   `json:"lastTickAt"`
	LastUserAt         int64   `json:"lastUserAt"`
	LastProactiveAt    int64   `json:"lastProactiveAt"`
	LastMemoryDecayAt  int64   `json:"lastMemoryDecayAt"`
}

func (s *Store) migrateLegacyState(id string, now int64) error {
	var rawJSON []byte
	if err := s.view(func(tx *bolt.Tx) error {
		rawJSON = append([]byte(nil), tx.Bucket(bucketState).Get([]byte(id))...)
		return nil
	}); err != nil {
		return err
	}
	if len(rawJSON) == 0 {
		return s.SaveState(id, DefaultState(now))
	}
	var legacy legacyState
	if err := json.Unmarshal(rawJSON, &legacy); err != nil {
		return fmt.Errorf("migrate companion state: %w", err)
	}
	metrics := defaultMetricValues()
	if legacy.Energy != 0 {
		metrics[MetricVitality] = legacy.Energy
	}
	if legacy.Hunger != 0 {
		metrics[MetricHunger] = legacy.Hunger
	}
	if legacy.Fatigue != 0 {
		metrics[MetricFatigue] = legacy.Fatigue
	}
	if legacy.Happiness != 0 {
		metrics[MetricMoodValence] = clampFloat((legacy.Happiness-50)*2, -100, 100)
	}
	if legacy.Anxiety != 0 {
		metrics[MetricAnxiety] = legacy.Anxiety
	}
	if legacy.Loneliness != 0 {
		metrics[MetricLoneliness] = legacy.Loneliness
	}
	if legacy.SocialSatisfaction != 0 {
		metrics[MetricSocialEnergy] = legacy.SocialSatisfaction
	}
	if legacy.Libido != 0 {
		metrics[MetricLibido] = legacy.Libido
	}
	if legacy.Stress != 0 {
		metrics[MetricStress] = legacy.Stress
	}
	metrics[MetricMissing] = legacy.MissingYou
	if legacy.Trust != 0 {
		metrics[MetricTrust] = legacy.Trust
	}
	if legacy.Intimacy != 0 {
		metrics[MetricIntimacy] = legacy.Intimacy
	}
	state := State{
		Metrics:           metrics,
		Traits:            defaultTraitValues(),
		CurrentActivity:   legacy.CurrentActivity,
		ActivityKind:      legacy.ActivityKind,
		ActivityStartedAt: legacy.ActivityStartedAt,
		ActivityEndsAt:    legacy.ActivityEndsAt,
		UpdatedAt:         legacy.UpdatedAt,
		LastTickAt:        legacy.LastTickAt,
		LastUserAt:        legacy.LastUserAt,
		LastProactiveAt:   legacy.LastProactiveAt,
		LastMemoryDecayAt: legacy.LastMemoryDecayAt,
	}
	return s.SaveState(id, normalizeState(state, now))
}

func (s *Store) loadTraits() map[TraitID]float64 {
	out := map[TraitID]float64{}
	_ = s.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketTraits).ForEach(func(key, raw []byte) error {
			var value float64
			if err := json.Unmarshal(raw, &value); err == nil {
				out[TraitID(string(key))] = value
			}
			return nil
		})
	})
	if len(out) == 0 {
		return defaultTraitValues()
	}
	return out
}

func putState(tx *bolt.Tx, id string, state State) error {
	traits := state.Traits
	state.Traits = nil
	if err := putJSON(tx.Bucket(bucketState), id, state); err != nil {
		return err
	}
	bucket := tx.Bucket(bucketTraits)
	for id, value := range traits {
		if err := putJSON(bucket, string(id), value); err != nil {
			return err
		}
	}
	return nil
}

func clampFloat(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func normalizeMemory(memory Memory) Memory {
	if memory.Strength <= 0 {
		memory.Strength = float64(memory.Importance)
	}
	if memory.Strength > 10 {
		memory.Strength = 10
	}
	return memory
}
