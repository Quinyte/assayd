package envtest

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// countingClient records how many writes a reconciler issues.
//
// It exists because the obvious idempotency assertion does not work: the API
// server does not bump resourceVersion when an update leaves the object
// unchanged, so comparing resourceVersions across repeated reconciles tests
// Kubernetes' write deduplication rather than the operator's. A controller that
// issues a full status write every pass would pass that assertion while
// hammering the API server and fighting every other writer of the object.
type countingClient struct {
	client.Client
	mu     sync.Mutex
	writes int
}

type countingStatusWriter struct {
	client.SubResourceWriter
	parent *countingClient
}

func (c *countingClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

func (c *countingClient) record() {
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
}

func (c *countingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.record()
	return c.Client.Create(ctx, obj, opts...)
}

func (c *countingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.record()
	return c.Client.Update(ctx, obj, opts...)
}

func (c *countingClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	c.record()
	return c.Client.Patch(ctx, obj, p, opts...)
}

func (c *countingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.record()
	return c.Client.Delete(ctx, obj, opts...)
}

func (c *countingClient) Status() client.SubResourceWriter {
	return &countingStatusWriter{SubResourceWriter: c.Client.Status(), parent: c}
}

func (w *countingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.parent.record()
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w *countingStatusWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	w.parent.record()
	return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
}
