package companion

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type registryFixture struct {
	registry      *Registry
	registryPath  string
	companionsDir string
	legacyPath    string
	conversation  *fakeConversation
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := &registryFixture{
		registryPath:  filepath.Join(dir, "companions.db"),
		companionsDir: filepath.Join(dir, "companions"),
		legacyPath:    filepath.Join(dir, "companion.db"),
		conversation:  &fakeConversation{threadID: "th_companion"},
	}
	registry, err := OpenRegistry(fixture.registryPath, fixture.companionsDir, fixture.legacyPath)
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	registry.Configure(&fakeModel{}, fixture.conversation)
	fixture.registry = registry
	t.Cleanup(func() { _ = registry.Close() })
	return fixture
}

func TestRegistrySeedsDefaultCharacterFromLegacyPath(t *testing.T) {
	fixture := newRegistryFixture(t)
	characters, err := fixture.registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(characters) != 1 {
		t.Fatalf("characters = %d, want 1", len(characters))
	}
	if characters[0].ID != DefaultID || !characters[0].Active {
		t.Fatalf("seeded character = %+v", characters[0])
	}
	record, ok := fixture.registry.record(DefaultID)
	if !ok {
		t.Fatal("seeded record missing")
	}
	if record.DBPath != fixture.legacyPath {
		t.Fatalf("seeded dbPath = %q, want legacy %q", record.DBPath, fixture.legacyPath)
	}
	manager, err := fixture.registry.Active()
	if err != nil {
		t.Fatal(err)
	}
	if manager.ID() != DefaultID {
		t.Fatalf("active id = %q, want %q", manager.ID(), DefaultID)
	}
}

func TestRegistryCreateBuildsWorldInCompanionsDir(t *testing.T) {
	fixture := newRegistryFixture(t)
	created, err := fixture.registry.Create(Profile{
		Name:    "小晴",
		Avatar:  "☀️",
		Summary: "明快、爱开玩笑、藏不住事",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.ID, "cmp_") {
		t.Fatalf("created id = %q, want cmp_ prefix", created.ID)
	}
	if created.Name != "小晴" || created.Avatar != "☀️" {
		t.Fatalf("created summary = %+v", created)
	}
	worldPath := filepath.Join(fixture.companionsDir, created.ID+".db")
	if _, err := os.Stat(worldPath); err != nil {
		t.Fatalf("new world database missing at %s: %v", worldPath, err)
	}
	// The default character was idle, so the new one should be active.
	if !created.Active {
		t.Fatal("new character was not auto-activated")
	}
	if fixture.registry.ActiveID() != created.ID {
		t.Fatalf("active id = %q, want %q", fixture.registry.ActiveID(), created.ID)
	}
	// The new world must carry the given persona, not the built-in default.
	manager, err := fixture.registry.Manager(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := manager.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if profile.Summary != "明快、爱开玩笑、藏不住事" {
		t.Fatalf("new world summary = %q", profile.Summary)
	}
}

func TestRegistrySwitchPersistsAcrossReopen(t *testing.T) {
	fixture := newRegistryFixture(t)
	created, err := fixture.registry.Create(Profile{Name: "知野", Summary: "成熟、克制、观察力强"})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.registry.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := OpenRegistry(fixture.registryPath, fixture.companionsDir, fixture.legacyPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if reopened.ActiveID() != created.ID {
		t.Fatalf("reopened active id = %q, want %q", reopened.ActiveID(), created.ID)
	}
	characters, err := reopened.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(characters) != 2 {
		t.Fatalf("characters after reopen = %d, want 2", len(characters))
	}
	if characters[0].ID != created.ID || !characters[0].Active {
		t.Fatalf("active character should sort first: %+v", characters[0])
	}
}

func TestRegistrySwitchRefusesBusyCharacter(t *testing.T) {
	fixture := newRegistryFixture(t)
	created, err := fixture.registry.Create(Profile{Name: "小晴", Summary: "明快"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.conversation.mu.Lock()
	fixture.conversation.running = true
	fixture.conversation.mu.Unlock()

	err = fixture.registry.Switch(DefaultID)
	if err == nil {
		t.Fatal("expected switch to be refused while the active character is replying")
	}
	if fixture.registry.ActiveID() != created.ID {
		t.Fatalf("active id changed despite refusal: %q", fixture.registry.ActiveID())
	}
}

func TestRegistryDeleteRemovesRecordAndFile(t *testing.T) {
	fixture := newRegistryFixture(t)
	created, err := fixture.registry.Create(Profile{Name: "小晴", Summary: "明快"})
	if err != nil {
		t.Fatal(err)
	}
	worldPath := filepath.Join(fixture.companionsDir, created.ID+".db")
	if err := fixture.registry.Delete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(worldPath); !os.IsNotExist(err) {
		t.Fatalf("world database still present: %v", err)
	}
	characters, err := fixture.registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(characters) != 1 || characters[0].ID != DefaultID {
		t.Fatalf("characters = %+v, want only the default", characters)
	}
	// Deleting the active character activates the most recently touched survivor.
	if fixture.registry.ActiveID() != DefaultID {
		t.Fatalf("active id = %q, want %q", fixture.registry.ActiveID(), DefaultID)
	}
}

func TestRegistryCannotDeleteLastCharacter(t *testing.T) {
	fixture := newRegistryFixture(t)
	if err := fixture.registry.Delete(DefaultID); err == nil {
		t.Fatal("expected deleting the last character to fail")
	}
	characters, _ := fixture.registry.List()
	if len(characters) != 1 {
		t.Fatalf("characters = %d, want 1", len(characters))
	}
}

func TestRegistryEnforcesCharacterCap(t *testing.T) {
	fixture := newRegistryFixture(t)
	for i := 0; i < MaxCharacters-1; i++ {
		if _, err := fixture.registry.Create(Profile{Name: "角色", Summary: "测试角色摘要"}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	characters, _ := fixture.registry.List()
	if len(characters) != MaxCharacters {
		t.Fatalf("characters = %d, want %d", len(characters), MaxCharacters)
	}
	if _, err := fixture.registry.Create(Profile{Name: "超额", Summary: "不该建出来"}); err == nil {
		t.Fatal("expected the cap to reject one more character")
	}
}

func TestCharacterWorldsStaySeparate(t *testing.T) {
	fixture := newRegistryFixture(t)
	defaultManager, err := fixture.registry.Manager(DefaultID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultManager.SaveMemory(Memory{Content: "只属于默认角色的记忆", Importance: 8}); err != nil {
		t.Fatal(err)
	}
	created, err := fixture.registry.Create(Profile{Name: "小晴", Summary: "明快"})
	if err != nil {
		t.Fatal(err)
	}
	newManager, err := fixture.registry.Manager(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if memories, err := newManager.ListMemories(0); err != nil {
		t.Fatal(err)
	} else if len(memories) != 0 {
		t.Fatalf("new character inherited %d memories", len(memories))
	}
	if memories, err := defaultManager.ListMemories(0); err != nil {
		t.Fatal(err)
	} else if len(memories) != 1 {
		t.Fatalf("default character memories = %d, want 1", len(memories))
	}
}

func TestSaveProfileKeepsCharacterID(t *testing.T) {
	fixture := newRegistryFixture(t)
	created, err := fixture.registry.Create(Profile{Name: "小晴", Summary: "明快"})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := fixture.registry.Manager(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := manager.SaveProfile(context.Background(), Profile{Name: "小晴改名", Summary: "还是明快"})
	if err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if saved.ID != created.ID {
		t.Fatalf("saved profile id = %q, want %q", saved.ID, created.ID)
	}
	// A non-default character must never fall back to the built-in default id.
	if saved.ID == DefaultID {
		t.Fatal("character profile collapsed onto the default id")
	}
	if err := fixture.registry.SyncProfile(saved); err != nil {
		t.Fatal(err)
	}
	characters, _ := fixture.registry.List()
	for _, character := range characters {
		if character.ID == created.ID && character.Name != "小晴改名" {
			t.Fatalf("registry name not synced: %q", character.Name)
		}
	}
}

func TestManagerForThreadRoutesToOwningCharacter(t *testing.T) {
	fixture := newRegistryFixture(t)
	manager, err := fixture.registry.Active()
	if err != nil {
		t.Fatal(err)
	}
	overview, err := manager.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.CharacterID != DefaultID {
		t.Fatalf("overview characterId = %q, want %q", overview.CharacterID, DefaultID)
	}
	found, err := fixture.registry.ManagerForThread(overview.ThreadID)
	if err != nil {
		t.Fatalf("route by thread: %v", err)
	}
	if found.ID() != DefaultID {
		t.Fatalf("routed to %q, want %q", found.ID(), DefaultID)
	}
	if _, err := fixture.registry.ManagerForThread("th_nobody"); err == nil {
		t.Fatal("expected unknown thread to have no owning character")
	}
}
