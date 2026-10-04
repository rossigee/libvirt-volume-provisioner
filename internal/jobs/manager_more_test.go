package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager builds a manager with only the dependencies a given test needs.
func newTestManager(t *testing.T, pool *mockLibvirtPool, store *storage.Store) *Manager {
	t.Helper()

	// A nil *mockLibvirtPool passed as a LibvirtPool would produce a non-nil
	// interface holding a typed nil, which bypasses the manager's `libvirtPool ==
	// nil` guards and panics on first use. Pass an untyped nil instead, so the
	// guard behaves as it does in production when the pool is not configured.
	var iface LibvirtPool
	if pool != nil {
		iface = pool
	}

	m := NewManager(nil, nil, iface, store, nil, 2, 30*time.Minute, 168*time.Hour, time.Hour, 0)
	t.Cleanup(m.Stop)
	return m
}

func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	return store
}

// -- syncToDatabase -----------------------------------------------------

// syncToDatabase persists a snapshot of the job. This is what makes a job
// survive a restart, so the fields it writes are the ones recovery depends on.
func TestSyncToDatabase_PersistsJobState(t *testing.T) {
	store := newTestStore(t)
	m := newTestManager(t, &mockLibvirtPool{}, store)

	req := types.ProvisionRequest{VolumeName: "runner-v4", ImageURL: "http://minio/img.qcow2"}
	job := &Job{
		ID:      "job-sync-1",
		Status:  types.StatusCompleted,
		Request: req,
		Progress: &types.ProgressInfo{
			Stage:          "populating",
			OverallPercent: 100,
		},
		CreatedAt: time.Now().Add(-time.Minute),
		UpdatedAt: time.Now(),
	}

	m.syncToDatabase(context.Background(), job)

	got, err := store.GetJob(context.Background(), "job-sync-1")
	require.NoError(t, err, "the job must be readable back after a sync")
	assert.Equal(t, string(types.StatusCompleted), got.Status)

	var roundTripped types.ProvisionRequest
	require.NoError(t, json.Unmarshal([]byte(got.RequestJSON), &roundTripped))
	assert.Equal(t, req, roundTripped, "the request must round-trip through JSON")

	require.NotNil(t, got.ProgressJSON)
	assert.Contains(t, got.ProgressJSON, "populating")

	// A terminal status is what marks the job as finished for recovery.
	require.NotNil(t, got.CompletedAt, "a completed job must record CompletedAt")
}

// An in-flight job is not finished, so CompletedAt must stay nil. Setting it
// early would make recovery treat a running job as finished.
func TestSyncToDatabase_InFlightJobHasNoCompletedAt(t *testing.T) {
	for _, status := range []types.JobStatus{
		types.StatusPending, types.StatusRunning,
	} {
		t.Run(string(status), func(t *testing.T) {
			store := newTestStore(t)
			m := newTestManager(t, &mockLibvirtPool{}, store)

			job := &Job{
				ID:        "job-inflight",
				Status:    status,
				Request:   types.ProvisionRequest{VolumeName: "v"},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			m.syncToDatabase(context.Background(), job)

			got, err := store.GetJob(context.Background(), "job-inflight")
			require.NoError(t, err)
			assert.Nil(t, got.CompletedAt,
				"status %s must not be recorded as finished", status)
		})
	}
}

// Every terminal status marks the job finished, otherwise a failed or cancelled
// job would be resurrected by recovery.
func TestSyncToDatabase_TerminalStatusesSetCompletedAt(t *testing.T) {
	for _, status := range []types.JobStatus{
		types.StatusCompleted, types.StatusFailed, types.StatusCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			store := newTestStore(t)
			m := newTestManager(t, &mockLibvirtPool{}, store)

			job := &Job{
				ID:        "job-terminal",
				Status:    status,
				Request:   types.ProvisionRequest{VolumeName: "v"},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			m.syncToDatabase(context.Background(), job)

			got, err := store.GetJob(context.Background(), "job-terminal")
			require.NoError(t, err)
			assert.NotNil(t, got.CompletedAt, "status %s must record CompletedAt", status)
		})
	}
}

func TestSyncToDatabase_ErrorMessageIsRecorded(t *testing.T) {
	store := newTestStore(t)
	m := newTestManager(t, &mockLibvirtPool{}, store)

	job := &Job{
		ID:        "job-failed",
		Status:    types.StatusFailed,
		Request:   types.ProvisionRequest{VolumeName: "v"},
		Error:     errors.New("lvcreate: insufficient free extents"),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	m.syncToDatabase(context.Background(), job)

	got, err := store.GetJob(context.Background(), "job-failed")
	require.NoError(t, err)
	assert.Contains(t, got.ErrorMessage, "insufficient free extents")
}

// No store configured must be a silent no-op, not a panic: the daemon is
// documented to run without persistence.
func TestSyncToDatabase_NilStoreIsNoOp(t *testing.T) {
	m := newTestManager(t, &mockLibvirtPool{}, nil)

	assert.NotPanics(t, func() {
		m.syncToDatabase(context.Background(), &Job{
			ID:      "job-nostore",
			Status:  types.StatusRunning,
			Request: types.ProvisionRequest{VolumeName: "v"},
		})
	})
}

// A job with no progress and no error must sync without those fields becoming
// misleading values.
func TestSyncToDatabase_NilProgressAndError(t *testing.T) {
	store := newTestStore(t)
	m := newTestManager(t, &mockLibvirtPool{}, store)

	job := &Job{
		ID:        "job-bare",
		Status:    types.StatusPending,
		Request:   types.ProvisionRequest{VolumeName: "v"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	m.syncToDatabase(context.Background(), job)

	got, err := store.GetJob(context.Background(), "job-bare")
	require.NoError(t, err)
	assert.Empty(t, got.ProgressJSON)
	assert.Empty(t, got.ErrorMessage)
}

// -- ListCachedImages ---------------------------------------------------

func TestListCachedImages_NilPoolIsAnError(t *testing.T) {
	m := newTestManager(t, nil, newTestStore(t))

	images, err := m.ListCachedImages()
	require.Error(t, err)
	assert.Nil(t, images)
	assert.Contains(t, err.Error(), "not initialized")
}

func TestListCachedImages_PropagatesPoolError(t *testing.T) {
	pool := &mockLibvirtPool{listErr: errors.New("pool directory unreadable")}
	m := newTestManager(t, pool, newTestStore(t))

	images, err := m.ListCachedImages()
	require.Error(t, err)
	assert.Nil(t, images)
	assert.Contains(t, err.Error(), "pool directory unreadable")
}

func TestListCachedImages_ReturnsPoolImages(t *testing.T) {
	pool := &mockLibvirtPool{listErr: nil}
	m := newTestManager(t, pool, newTestStore(t))

	// The mock reports no error, so the manager must not invent one: a spurious
	// failure here would surface as a 500 on a working host.
	images, err := m.ListCachedImages()
	require.NoError(t, err)
	assert.NotNil(t, images)
}

// -- DeleteCachedImage ---------------------------------------------------

// Key validation lives in the HTTP handler and again in the pool's
// AllocateImageFile; this layer neither validates nor rewrites the key, so the
// test pins that it passes through untouched rather than asserting a check that
// does not exist here.
func TestDeleteCachedImage_PassesKeyThroughUnchanged(t *testing.T) {
	cacheDir := t.TempDir()
	pool := &mockLibvirtPool{cacheDir: cacheDir}
	m := newTestManager(t, pool, newTestStore(t))

	key := "a1b2c3d4" + strings.Repeat("e5", 28) // 64 lowercase hex chars
	require.Len(t, key, 64)

	require.NoError(t, m.DeleteCachedImage(key))

	// The path handed to the pool must be the one the key resolves to, with no
	// re-encoding: the cache key is the on-disk filename.
	assert.Equal(t, []string{filepath.Join(cacheDir, key)}, pool.deletedPaths)
}

// A failure to resolve the path must be reported, not swallowed into a
// successful-looking delete.
func TestDeleteCachedImage_PropagatesPoolError(t *testing.T) {
	pool := &mockLibvirtPool{allocateErr: errors.New("invalid cache key")}
	m := newTestManager(t, pool, newTestStore(t))

	err := m.DeleteCachedImage("not-a-valid-key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve cache path")
	assert.Contains(t, err.Error(), "invalid cache key")
}
