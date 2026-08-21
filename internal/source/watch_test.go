package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/integrio-intropy/intropy-cli/internal/registry"
)

// flakyResolver fails with ErrNotFound until its underlying registry holds the
// tag — the shape CI's eventual consistency takes in the watch tests.
type flakyResolver struct {
	registry *registry.Client
	calls    atomic.Int32
}

func (f *flakyResolver) Resolve(ctx context.Context, ref string) (registry.Descriptor, error) {
	f.calls.Add(1)
	return f.registry.Resolve(ctx, ref)
}

// failResolver always returns the same error.
type failResolver struct{ err error }

func (s failResolver) Resolve(context.Context, string) (registry.Descriptor, error) {
	return registry.Descriptor{}, s.err
}

func TestWatchResolveDigestsWaitsForThePipeline(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	image := srv.Host + "/integrations/order-extractor"
	resolver := &flakyResolver{registry: c}

	var stderr bytes.Buffer
	result := make(chan error, 1)
	go func() {
		pins, err := WatchResolveDigests(ctx, resolver, componentWithImages(image), testCommit, WatchOptions{
			PollInterval: time.Millisecond,
			Stderr:       &stderr,
		})
		if err != nil {
			result <- err
			return
		}
		if len(pins) != 1 {
			result <- fmt.Errorf("pins = %+v, want 1", pins)
			return
		}
		result <- nil
	}()

	// CI finishes a moment later.
	time.Sleep(20 * time.Millisecond)
	if _, err := c.PushArtifact(ctx, image+":"+CommitTag(testCommit), testImageArtifact("amd64")); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not return after the image was published")
	}

	if resolver.calls.Load() < 2 {
		t.Errorf("calls = %d, want at least 2 (a miss, then a hit)", resolver.calls.Load())
	}
	for _, want := range []string{"waiting for the " + CommitTag(testCommit), "resolved after"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr should mention %q, got:\n%s", want, stderr.String())
		}
	}
}

// An image that is already there resolves on the first ask, with no waiting
// and no progress output — the watch flag must not slow down the common case.
func TestWatchResolveDigestsReturnsImmediatelyWhenPresent(t *testing.T) {
	c, srv := testRegistry(t)
	ctx := context.Background()
	image := srv.Host + "/integrations/order-extractor"
	if _, err := c.PushArtifact(ctx, image+":"+CommitTag(testCommit), testImageArtifact("amd64")); err != nil {
		t.Fatal(err)
	}
	resolver := &flakyResolver{registry: c}

	var stderr bytes.Buffer
	start := time.Now()
	pins, err := WatchResolveDigests(ctx, resolver, componentWithImages(image), testCommit, WatchOptions{
		PollInterval: time.Hour, // if it ever waits, the test would hang
		Stderr:       &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 1 {
		t.Fatalf("pins = %+v, want 1", pins)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want an immediate return", elapsed)
	}
	if resolver.calls.Load() != 1 {
		t.Errorf("calls = %d, want exactly 1", resolver.calls.Load())
	}
	if stderr.String() != "" {
		t.Errorf("stderr = %q, want silence when nothing was waited for", stderr.String())
	}
}

// Ctrl+C cancels the context; the wait must end with the cancellation rather
// than polling on.
func TestWatchResolveDigestsStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var stderr bytes.Buffer
	result := make(chan error, 1)
	go func() {
		_, err := WatchResolveDigests(ctx, failResolver{err: registry.ErrNotFound},
			componentWithImages("harbor.intropy.io/integrations/order-extractor"), testCommit, WatchOptions{
				PollInterval: time.Millisecond,
				Stderr:       &stderr,
			})
		result <- err
	}()

	// Let it poll at least once, then interrupt.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not stop after cancellation")
	}
}

// Only a missing tag is retried. Anything else — auth, network, garbage —
// will not improve by waiting and must fail on the spot.
func TestWatchResolveDigestsDoesNotRetryOtherFailures(t *testing.T) {
	var stderr bytes.Buffer
	_, err := WatchResolveDigests(context.Background(), failResolver{err: registry.ErrUnauthorized},
		componentWithImages("harbor.intropy.io/integrations/order-extractor"), testCommit, WatchOptions{
			PollInterval: time.Millisecond,
			Stderr:       &stderr,
		})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, registry.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(stderr.String(), "waiting") {
		t.Errorf("stderr %q should not announce a wait that never happened", stderr.String())
	}
}

// A first failure under a different error than the watch handles must surface
// the friendly pipeline message, so a bare (non-watch) failure reads the same
// as before — and errors.Is still finds the sentinel for the watch loop.
func TestResolveDigestsNotFoundKeepsTheSentinel(t *testing.T) {
	_, err := ResolveDigests(context.Background(), failResolver{err: registry.ErrNotFound},
		componentWithImages("harbor.intropy.io/integrations/order-extractor"), testCommit)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("err %q should wrap ErrNotFound so the watch loop can recognise it", err)
	}
	if !strings.Contains(err.Error(), "pipeline has not published") {
		t.Errorf("err %q should keep the friendly pipeline message", err)
	}
	if strings.Contains(err.Error(), "registry: not found") {
		t.Errorf("err %q should not grow the registry's wording as a tail", err)
	}
}

// The "still waiting" line appears only after the progress interval, so a slow
// build shows life without a line per poll.
func TestWatchResolveDigestsReportsProgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stderr bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = WatchResolveDigests(ctx, failResolver{err: registry.ErrNotFound},
			componentWithImages("harbor.intropy.io/integrations/order-extractor"), testCommit, WatchOptions{
				PollInterval:     time.Millisecond,
				progressInterval: 10 * time.Millisecond,
				Stderr:           &stderr,
			})
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if !strings.Contains(stderr.String(), "still waiting") {
		t.Errorf("stderr should report progress, got:\n%s", stderr.String())
	}
}
