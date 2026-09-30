package companion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"
)

// MaxCharacters caps how many companion worlds one install may hold. Each world
// is a separate database and conversation, so the cap keeps file handles,
// switch cost and the switcher UI sane.
const MaxCharacters = 8

var (
	bucketCharacters   = []byte("characters")
	bucketRegistryMeta = []byte("meta")
)

const (
	registryActiveID = "active_id"
	registrySeeded   = "seeded"
)

// characterRecord is the registry's durable pointer to one companion world.
type characterRecord struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Avatar          string `json:"avatar,omitempty"`
	DBPath          string `json:"dbPath"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	LastActivatedAt int64  `json:"lastActivatedAt,omitempty"`
}

func (r characterRecord) summary(activeID string) CharacterSummary {
	return CharacterSummary{
		ID:              r.ID,
		Name:            r.Name,
		Avatar:          r.Avatar,
		Active:          r.ID == activeID,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
		LastActivatedAt: r.LastActivatedAt,
	}
}

// Registry owns the character index and the live managers behind it. Exactly one
// character is active at a time: only the active manager runs its tick and task
// loops, so inactive worlds stay frozen until they are switched back to.
type Registry struct {
	mu sync.Mutex
	db *bolt.DB

	companionsDir string
	managers      map[string]*Manager
	activeID      string

	model        ModelPort
	conversation ConversationPort
	emit         func(Event)
	logger       *slog.Logger
	ctx          context.Context
}

// OpenRegistry opens the character index. When the index is empty it seeds the
// built-in default character from legacyPath, which reuses an existing single
// companion database in place instead of migrating it.
func OpenRegistry(path, companionsDir, legacyPath string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("companion registry path is empty")
	}
	if err := os.MkdirAll(companionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create companions dir: %w", err)
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketCharacters); err != nil {
			return err
		}
		meta, err := tx.CreateBucketIfNotExists(bucketRegistryMeta)
		if err != nil {
			return err
		}
		if meta.Get([]byte(registrySeeded)) != nil {
			return nil
		}
		now := time.Now().UnixMilli()
		seed := characterRecord{
			ID:              DefaultID,
			Name:            DefaultName,
			Avatar:          "🌙",
			DBPath:          legacyPath,
			CreatedAt:       now,
			UpdatedAt:       now,
			LastActivatedAt: now,
		}
		if err := putRegistryJSON(tx.Bucket(bucketCharacters), seed.ID, seed); err != nil {
			return err
		}
		if err := meta.Put([]byte(registryActiveID), []byte(DefaultID)); err != nil {
			return err
		}
		return meta.Put([]byte(registrySeeded), []byte("1"))
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	registry := &Registry{
		db:            db,
		companionsDir: companionsDir,
		managers:      map[string]*Manager{},
	}
	active, err := registry.metaValue(registryActiveID)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, ok := registry.record(active); !ok {
		records, recordErr := registry.records()
		if recordErr != nil {
			_ = db.Close()
			return nil, recordErr
		}
		if len(records) == 0 {
			_ = db.Close()
			return nil, errors.New("companion registry holds no characters")
		}
		active = records[0].ID
		_ = registry.setMeta(registryActiveID, active)
	}
	registry.activeID = active
	return registry, nil
}

// Configure wires the shared ports. Both bridges are character-agnostic: the
// profile travels with every call, so one pair serves every world.
func (r *Registry) Configure(model ModelPort, conversation ConversationPort) {
	r.mu.Lock()
	r.model = model
	r.conversation = conversation
	for _, manager := range r.managers {
		manager.SetPorts(r.model, r.conversation)
	}
	r.mu.Unlock()
}

// SetEmitter wires the Wails event publisher for every current and future world.
func (r *Registry) SetEmitter(emit func(Event)) {
	r.mu.Lock()
	r.emit = emit
	for _, manager := range r.managers {
		manager.SetEmitter(emit)
	}
	r.mu.Unlock()
}

// SetLogger attaches the application logger to every current and future world.
func (r *Registry) SetLogger(logger *slog.Logger) {
	r.mu.Lock()
	r.logger = logger
	for _, manager := range r.managers {
		manager.SetLogger(logger)
	}
	r.mu.Unlock()
}

// Start launches the active character's loops. Inactive worlds stay stopped.
func (r *Registry) Start(ctx context.Context) {
	r.mu.Lock()
	r.ctx = ctx
	manager, err := r.managerLocked(r.activeID)
	r.mu.Unlock()
	if err == nil && manager != nil {
		manager.Start(ctx)
	}
}

// Close stops and closes every open world and the registry itself.
func (r *Registry) Close() error {
	r.mu.Lock()
	managers := make([]*Manager, 0, len(r.managers))
	for id, manager := range r.managers {
		managers = append(managers, manager)
		delete(r.managers, id)
	}
	r.mu.Unlock()

	var firstErr error
	for _, manager := range managers {
		if err := manager.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return firstErr
	}
	err := r.db.Close()
	r.db = nil
	if firstErr != nil {
		return firstErr
	}
	return err
}

// ActiveID returns the one character whose world is live.
func (r *Registry) ActiveID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeID
}

// Active returns the manager for the active character.
func (r *Registry) Active() (*Manager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.managerLocked(r.activeID)
}

// Manager returns one character's manager, opening its database on first use.
func (r *Registry) Manager(id string) (*Manager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.managerLocked(id)
}

// ManagerForThread finds the character that owns a companion conversation. Only
// open worlds can be matched, which is enough because a turn only ever runs in a
// world the registry already has open.
func (r *Registry) ManagerForThread(threadID string) (*Manager, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("thread id is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, manager := range r.managers {
		stored, _ := manager.store.Meta(metaPrimaryThread)
		if stored != "" && stored == threadID {
			return manager, nil
		}
	}
	return nil, fmt.Errorf("没有陪伴角色拥有会话 %q", threadID)
}

// List returns every character, active first and then most recently touched.
func (r *Registry) List() ([]CharacterSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.records()
	if err != nil {
		return nil, err
	}
	out := make([]CharacterSummary, 0, len(records))
	for _, record := range records {
		out = append(out, record.summary(r.activeID))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

// Create builds a new world for profile and registers it. When the current
// character is idle the new one is activated too; otherwise it is created
// without disturbing the active world.
func (r *Registry) Create(profile Profile) (CharacterSummary, error) {
	r.mu.Lock()
	records, err := r.records()
	if err != nil {
		r.mu.Unlock()
		return CharacterSummary{}, err
	}
	if len(records) >= MaxCharacters {
		r.mu.Unlock()
		return CharacterSummary{}, fmt.Errorf("最多只能创建 %d 个角色", MaxCharacters)
	}
	now := time.Now().UnixMilli()
	id := "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	dbPath := filepath.Join(r.companionsDir, id+".db")
	profile.ID = id
	profile.CreatedAt = now
	profile.UpdatedAt = now
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = DefaultName
	}
	if strings.TrimSpace(profile.Summary) == "" {
		profile.Summary = DefaultSummary
	}
	switch profile.ScheduleTendency {
	case "relaxed", "balanced", "busy", "social":
	default:
		profile.ScheduleTendency = "balanced"
	}

	manager, err := r.openManagerLocked(characterRecord{
		ID: id, Name: profile.Name, Avatar: profile.Avatar, DBPath: dbPath,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		r.mu.Unlock()
		return CharacterSummary{}, err
	}
	// Register the live handle now, so a later activate() reuses it instead of
	// opening a second bbolt instance on the same file.
	r.managers[id] = manager
	savedProfile, err := manager.SaveProfile(context.Background(), profile)
	if err != nil {
		delete(r.managers, id)
		_ = manager.Close()
		r.mu.Unlock()
		return CharacterSummary{}, err
	}
	record := characterRecord{
		ID: id, Name: savedProfile.Name, Avatar: savedProfile.Avatar, DBPath: dbPath,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := r.putRecord(record); err != nil {
		delete(r.managers, id)
		_ = manager.Close()
		r.mu.Unlock()
		return CharacterSummary{}, err
	}
	r.mu.Unlock()

	// Auto-activate only when the current world is idle, so a character that is
	// mid-reply keeps its turn intact and the user can switch later.
	summary := record.summary(r.ActiveID())
	if current, activeErr := r.Active(); activeErr == nil && current != nil {
		if busy, busyErr := current.Busy(); busyErr == nil && !busy {
			if switchErr := r.Switch(id); switchErr == nil {
				summary.Active = true
			}
		}
	} else if activateErr := r.activate(id); activateErr == nil {
		summary.Active = true
	}
	return summary, nil
}

// Switch makes id the only active character. It refuses to walk away from a
// character that is mid-response.
func (r *Registry) Switch(id string) error {
	r.mu.Lock()
	if _, ok := r.record(id); !ok {
		r.mu.Unlock()
		return fmt.Errorf("角色 %q 不存在", id)
	}
	previous := r.managers[r.activeID]
	r.mu.Unlock()

	if previous != nil && previous.ID() != id {
		busy, err := previous.Busy()
		if err != nil {
			return err
		}
		if busy {
			return errors.New("她正在回复，先等这一轮结束")
		}
	}
	return r.activate(id)
}

// activate performs the switch without a busy check on the outgoing character.
func (r *Registry) activate(id string) error {
	r.mu.Lock()
	record, ok := r.record(id)
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("角色 %q 不存在", id)
	}
	previousID := r.activeID
	previous := r.managers[previousID]
	manager, err := r.managerLocked(id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	r.activeID = id
	record.LastActivatedAt = time.Now().UnixMilli()
	if err := r.putRecord(record); err != nil {
		r.mu.Unlock()
		return err
	}
	if err := r.setMeta(registryActiveID, id); err != nil {
		r.mu.Unlock()
		return err
	}
	ctx := r.ctx
	r.mu.Unlock()

	if previous != nil && previousID != id {
		_ = previous.Stop()
	}
	if ctx != nil {
		manager.Start(ctx)
	}
	r.emitChanged(EventStateUpdated)
	return nil
}

// Delete removes a character, its world database and its registry record. The
// last remaining character cannot be deleted, and a busy character must finish
// first. Deleting the active character activates the most recently touched one.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	records, err := r.records()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if len(records) <= 1 {
		r.mu.Unlock()
		return errors.New("至少保留一个角色")
	}
	record, ok := r.record(id)
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("角色 %q 不存在", id)
	}
	manager, err := r.managerLocked(id)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	wasActive := r.activeID == id
	r.mu.Unlock()

	busy, err := manager.Busy()
	if err != nil {
		return err
	}
	if busy {
		return errors.New("她正在回复，暂时不能删除")
	}

	r.mu.Lock()
	if err := r.deleteRecord(id); err != nil {
		r.mu.Unlock()
		return err
	}
	delete(r.managers, id)
	remaining, err := r.records()
	r.mu.Unlock()
	if err != nil {
		return err
	}

	_ = manager.Close()
	if err := removeWorldFiles(record.DBPath); err != nil {
		return err
	}
	if wasActive && len(remaining) > 0 {
		next := remaining[0]
		for _, candidate := range remaining[1:] {
			if candidate.UpdatedAt > next.UpdatedAt {
				next = candidate
			}
		}
		return r.activate(next.ID)
	}
	r.emitChanged(EventStateUpdated)
	return nil
}

// SyncProfile keeps the registry's cached name and avatar in step with a
// profile edit made through the active manager.
func (r *Registry) SyncProfile(profile Profile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.record(profile.ID)
	if !ok {
		return nil
	}
	record.Name = profile.Name
	record.Avatar = profile.Avatar
	record.UpdatedAt = profile.UpdatedAt
	return r.putRecord(record)
}

func (r *Registry) managerLocked(id string) (*Manager, error) {
	if manager, ok := r.managers[id]; ok {
		return manager, nil
	}
	record, ok := r.record(id)
	if !ok {
		return nil, fmt.Errorf("角色 %q 不存在", id)
	}
	manager, err := r.openManagerLocked(record)
	if err != nil {
		return nil, err
	}
	r.managers[id] = manager
	return manager, nil
}

func (r *Registry) openManagerLocked(record characterRecord) (*Manager, error) {
	manager, err := NewManagerWithID(record.DBPath, record.ID)
	if err != nil {
		return nil, err
	}
	manager.SetPorts(r.model, r.conversation)
	manager.SetEmitter(r.emit)
	manager.SetLogger(r.logger)
	return manager, nil
}

func (r *Registry) emitChanged(kind EventKind) {
	r.mu.Lock()
	emit := r.emit
	manager := r.managers[r.activeID]
	r.mu.Unlock()
	if emit == nil || manager == nil {
		return
	}
	if overview, err := manager.overviewUnchecked(context.Background()); err == nil {
		emit(Event{Kind: kind, CharacterID: manager.ID(), Overview: overview, At: time.Now().UnixMilli()})
	}
}

func (r *Registry) records() ([]characterRecord, error) {
	var records []characterRecord
	err := r.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCharacters).ForEach(func(_, raw []byte) error {
			var record characterRecord
			if err := json.Unmarshal(raw, &record); err == nil && record.ID != "" {
				records = append(records, record)
			}
			return nil
		})
	})
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt < records[j].CreatedAt })
	return records, err
}

func (r *Registry) record(id string) (characterRecord, bool) {
	var found characterRecord
	if strings.TrimSpace(id) == "" {
		return characterRecord{}, false
	}
	_ = r.view(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketCharacters).Get([]byte(id))
		if len(raw) == 0 {
			return nil
		}
		return json.Unmarshal(raw, &found)
	})
	if found.ID == "" {
		return characterRecord{}, false
	}
	return found, true
}

func (r *Registry) putRecord(record characterRecord) error {
	return r.update(func(tx *bolt.Tx) error {
		return putRegistryJSON(tx.Bucket(bucketCharacters), record.ID, record)
	})
}

func (r *Registry) deleteRecord(id string) error {
	return r.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCharacters).Delete([]byte(id))
	})
}

func (r *Registry) metaValue(key string) (string, error) {
	var value string
	err := r.view(func(tx *bolt.Tx) error {
		value = string(tx.Bucket(bucketRegistryMeta).Get([]byte(key)))
		return nil
	})
	return value, err
}

func (r *Registry) setMeta(key, value string) error {
	return r.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketRegistryMeta).Put([]byte(key), []byte(value))
	})
}

func (r *Registry) view(fn func(*bolt.Tx) error) error {
	if r.db == nil {
		return errors.New("companion registry is closed")
	}
	return r.db.View(fn)
}

func (r *Registry) update(fn func(*bolt.Tx) error) error {
	if r.db == nil {
		return errors.New("companion registry is closed")
	}
	return r.db.Update(fn)
}

func putRegistryJSON(bucket *bolt.Bucket, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return bucket.Put([]byte(key), raw)
}

// removeWorldFiles deletes a character database and its sidecar files. The
// default character's database lives outside companionsDir and is removed too,
// because the user asked for the whole world to go.
func removeWorldFiles(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete companion database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-lock"} {
		_ = os.Remove(path + suffix)
	}
	return nil
}
