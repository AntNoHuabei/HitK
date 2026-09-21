package kb

import (
	"context"

	"github.com/hitk/hitk/internal/provider"
)

// localEmbedder adapts the provider package's offline hashed model to the
// store's Embedder interface. It exists so the kb package never has to branch
// on "is there a remote embedder", and so the on-disk format stays independent
// of whichever client is configured.
type localEmbedder struct {
	inner *provider.HashEmbedder
}

// newLocalEmbedder returns the offline embedder.
func newLocalEmbedder() *localEmbedder {
	return &localEmbedder{inner: provider.NewHashEmbedder(512)}
}

// ID implements [Embedder].
func (l *localEmbedder) ID() string { return l.inner.ID() }

// Dimensions implements [Embedder].
func (l *localEmbedder) Dimensions() int { return l.inner.Dimensions() }

// Embed implements [Embedder].
func (l *localEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return l.inner.Embed(ctx, texts)
}
