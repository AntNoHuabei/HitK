package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hitk/hitk/internal/document"
	"github.com/hitk/hitk/internal/kb"
	"github.com/hitk/hitk/internal/skillruntime"
)

type fakeDocumentExtractor struct {
	calls  int
	errors []error
	result document.Result
}

func (f *fakeDocumentExtractor) Extract(context.Context, string, string) (document.Result, error) {
	index := f.calls
	f.calls++
	if index < len(f.errors) && f.errors[index] != nil {
		return document.Result{}, f.errors[index]
	}
	return f.result, nil
}

type fakeEnvironmentWaiter struct {
	calls int
	err   error
}

func (f *fakeEnvironmentWaiter) WaitSkillEnvironment(context.Context, string) (skillruntime.SkillEnvironmentStatus, error) {
	f.calls++
	if f.err != nil {
		return skillruntime.SkillEnvironmentStatus{}, f.err
	}
	return skillruntime.SkillEnvironmentStatus{Skill: "document", State: "ready"}, nil
}

func TestExtractDocumentWithRuntimeWaitsAndRetries(t *testing.T) {
	extractor := &fakeDocumentExtractor{
		errors: []error{skillruntime.ErrNetworkApprovalRequired},
		result: document.Result{Text: "converted", Mime: "application/pdf"},
	}
	waiter := &fakeEnvironmentWaiter{}

	result, err := extractDocumentWithRuntime(context.Background(), extractor, waiter, "scan.pdf", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "converted" || extractor.calls != 2 || waiter.calls != 1 {
		t.Fatalf("unexpected retry result: result=%#v extractor=%d waiter=%d", result, extractor.calls, waiter.calls)
	}
}

func TestExtractDocumentWithRuntimePropagatesWaiterFailure(t *testing.T) {
	extractor := &fakeDocumentExtractor{errors: []error{skillruntime.ErrNetworkApprovalRequired}}
	waiter := &fakeEnvironmentWaiter{err: skillruntime.ErrComponentCancelled}

	_, err := extractDocumentWithRuntime(context.Background(), extractor, waiter, "scan.pdf", t.TempDir())
	if !errors.Is(err, skillruntime.ErrComponentCancelled) {
		t.Fatalf("wait error = %v", err)
	}
	if extractor.calls != 1 || waiter.calls != 1 {
		t.Fatalf("unexpected call counts: extractor=%d waiter=%d", extractor.calls, waiter.calls)
	}
}

func TestKBAddFilesUsesParserCallback(t *testing.T) {
	store, err := kb.Open(filepath.Join(t.TempDir(), "kb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed := false
	store.SetFileParser(func(context.Context, string) (*kb.ParsedDocument, error) {
		parsed = true
		return &kb.ParsedDocument{Title: "notes", Text: "parser callback content", Mime: "text/plain"}, nil
	}, func(string) bool { return true })

	collection, err := store.CreateCollection("docs", "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewKBService(&Container{
		KB:        store,
		Documents: document.NewEngine(nil),
	})
	result, err := service.AddFiles(collection.ID, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	if !parsed || result.Added != 1 || len(result.Failed) != 0 {
		t.Fatalf("parser boundary was not used: parsed=%v result=%#v", parsed, result)
	}
}
