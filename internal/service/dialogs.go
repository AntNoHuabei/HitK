package service

import (
	"context"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// contextWithTimeout is a small helper so service methods never forget to
// release the context they create for a network probe.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// pickDirectory shows the native folder picker.
//
// Wails returns an error when the user cancels; callers treat that as "no
// selection" rather than a failure, which is why the wrapper swallows nothing
// here and lets the caller decide.
func pickDirectory(title string) (string, error) {
	app := application.Get()
	dialog := app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true)
	return dialog.PromptForSingleSelection()
}

// pickFiles shows the native multi-file picker.
func pickFiles(title string) ([]string, error) {
	app := application.Get()
	dialog := app.Dialog.OpenFile().
		SetTitle(title).
		CanChooseFiles(true).
		CanChooseDirectories(false)
	// Filters are added as one broad entry: the knowledge base accepts code,
	// prose, tables and PDFs, so a long filter list would only get in the way.
	for _, group := range []struct{ name, pattern string }{
		{"文档与代码", "*.md;*.markdown;*.txt;*.pdf;*.docx;*.csv;*.tsv;*.json;*.yaml;*.yml;*.html"},
		{"全部文件", "*.*"},
	} {
		dialog = dialog.AddFilter(group.name, group.pattern)
	}
	return dialog.PromptForMultipleSelection()
}

// pickSaveFile shows the native save dialog, used when exporting a thread.
func pickSaveFile(title, defaultName string) (string, error) {
	app := application.Get()
	// SaveFileDialogStruct exposes SetMessage rather than SetTitle, so the
	// prompt text carries the title.
	return app.Dialog.SaveFile().
		SetMessage(title).
		SetFilename(defaultName).
		AddFilter("Markdown", "*.md").
		PromptForSingleSelection()
}
