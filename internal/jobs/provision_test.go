package jobs

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
	"github.com/rossigee/libvirt-volume-provisioner/internal/minio"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLVM records what the provisioning flow asked for, and can fail any step.
//
// This type only compiles because Manager depends on the LVMManager interface
// rather than on *lvm.Manager. Before that change the create, populate and
// rollback paths could not be exercised at all without a real volume group.
type fakeLVM struct {
	mu sync.Mutex

	createCalls   int
	populateCalls int
	deleteCalls   int

	order          []string // ordered log of lvm calls, e.g. ["create", "populate"]
	createdVolume  string
	createdSizeGB  int
	populatedImage string
	populatedType  string
	populatedJobID string
	deletedVolumes []string

	createErr   error
	populateErr error
	deleteErr   error
}

var _ LVMManager = (*fakeLVM)(nil)

func (f *fakeLVM) CreateVolume(_ context.Context, volumeName string, sizeGB int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.order = append(f.order, "create")
	f.createdVolume = volumeName
	f.createdSizeGB = sizeGB
	return f.createErr
}

func (f *fakeLVM) PopulateVolume(_ context.Context,
	imagePath, volumeName, imageType string,
	_ lvm.ProgressUpdater, _ *storage.Store, jobID string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.populateCalls++
	f.order = append(f.order, "populate")
	f.populatedImage = imagePath
	f.createdVolume = volumeName
	f.populatedType = imageType
	f.populatedJobID = jobID
	return f.populateErr
}

func (f *fakeLVM) DeleteVolume(_ context.Context, volumeName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.order = append(f.order, "delete")
	f.deletedVolumes = append(f.deletedVolumes, volumeName)
	return f.deleteErr
}

// lvmCalls is a point-in-time copy of what the fake recorded.
type lvmCalls struct {
	create   int
	populate int
	delete   int
	deleted  []string
}

func (f *fakeLVM) snapshot() lvmCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return lvmCalls{
		create:   f.createCalls,
		populate: f.populateCalls,
		delete:   f.deleteCalls,
		deleted:  append([]string(nil), f.deletedVolumes...),
	}
}

// fakeMinio serves image content from memory.
type fakeMinio struct {
	content []byte

	mu        sync.Mutex
	downloads int
	err       error
}

var _ MinioClient = (*fakeMinio)(nil)

func (f *fakeMinio) GetObjectContent(context.Context, string, string) ([]byte, error) {
	return f.content, nil
}

func (f *fakeMinio) DownloadImageToPath(_ context.Context, _, destPath string, _ minio.ProgressUpdater) error {
	f.mu.Lock()
	f.downloads++
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFile(destPath, f.content)
}

func newProvisioningManager(t *testing.T, lvmMock *fakeLVM, minioMock *fakeMinio) *Manager {
	t.Helper()
	pool := &mockLibvirtPool{cacheDir: t.TempDir()}

	m := NewManager(minioMock, lvmMock, pool, newTestStore(t), nil,
		2, 30*time.Minute, 168*time.Hour, time.Hour, 0)
	t.Cleanup(m.Stop)
	return m
}

func provisionReq() types.ProvisionRequest {
	return types.ProvisionRequest{
		VolumeName:   "runner-v4",
		VolumeSizeGB: 1,
		ImageURL:     "http://minio/images/base/ubuntu.qcow2",
		ImageType:    "qcow2",
	}
}

// -- DeleteVolume -------------------------------------------------------

func TestDeleteVolume_DelegatesToLVM(t *testing.T) {
	lvmMock := &fakeLVM{}
	m := newTestManager(t, nil, newTestStore(t))
	m.lvmManager = lvmMock

	require.NoError(t, m.DeleteVolume(context.Background(), "runner-v4"))

	calls := lvmMock.snapshot()
	assert.Equal(t, 1, calls.delete)
	assert.Equal(t, []string{"runner-v4"}, calls.deleted)
}

func TestDeleteVolume_WrapsError(t *testing.T) {
	lvmMock := &fakeLVM{deleteErr: errors.New("volume in use")}
	m := newTestManager(t, nil, newTestStore(t))
	m.lvmManager = lvmMock

	err := m.DeleteVolume(context.Background(), "runner-v4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete volume runner-v4")
	assert.Contains(t, err.Error(), "volume in use")
}

// -- ProvisionVolume: the happy path ------------------------------------

func TestProvisionVolume_CreatesAndPopulates(t *testing.T) {
	lvmMock := &fakeLVM{}
	minioMock := &fakeMinio{content: []byte("image-bytes")}
	m := newProvisioningManager(t, lvmMock, minioMock)

	job := &Job{ID: "job-1", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, m.ProvisionVolume(context.Background(), job))

	calls := lvmMock.snapshot()
	assert.Equal(t, 1, calls.create)
	assert.Equal(t, 1, calls.populate)
	assert.Equal(t, 0, calls.delete, "a successful provision must not delete anything")

	assert.Equal(t, "runner-v4", lvmMock.createdVolume)
	assert.Equal(t, 1, lvmMock.createdSizeGB)
	assert.Equal(t, "qcow2", lvmMock.populatedType)
	assert.Equal(t, "job-1", lvmMock.populatedJobID)
	assert.NotEmpty(t, lvmMock.populatedImage, "populate must receive the acquired image path")
}

// The volume must be created before it is populated. Populating a volume that
// does not exist is the "Cannot grow device files" class of failure.
func TestProvisionVolume_CreatesBeforePopulating(t *testing.T) {
	lvmMock := &fakeLVM{}
	minioMock := &fakeMinio{content: []byte("image-bytes")}
	m := newProvisioningManager(t, lvmMock, minioMock)

	job := &Job{ID: "job-order", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, m.ProvisionVolume(context.Background(), job))

	// Counts cannot express ordering, so assert on the recorded call sequence.
	assert.Equal(t, []string{"create", "populate"}, lvmMock.order,
		"the volume must be created before it is populated")
}

// -- ProvisionVolume: rollback -----------------------------------------

// A volume created and then not populated must be reclaimed, or every failed
// provision leaks an LVM volume.
func TestProvisionVolume_RollsBackWhenPopulateFails(t *testing.T) {
	lvmMock := &fakeLVM{populateErr: errors.New("qemu-img: Cannot grow device files")}
	minioMock := &fakeMinio{content: []byte("image-bytes")}
	m := newProvisioningManager(t, lvmMock, minioMock)

	job := &Job{ID: "job-rollback", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := m.ProvisionVolume(context.Background(), job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to populate volume")

	calls := lvmMock.snapshot()
	assert.Equal(t, 1, calls.delete, "the volume must be reclaimed after a failed populate")
	assert.Equal(t, []string{"runner-v4"}, calls.deleted)
}

// Nothing was created, so nothing may be deleted. Rolling back a volume that was
// never made would delete an unrelated volume with the same name.
func TestProvisionVolume_NoRollbackWhenCreateFails(t *testing.T) {
	lvmMock := &fakeLVM{createErr: errors.New("not enough free extents")}
	minioMock := &fakeMinio{content: []byte("image-bytes")}
	m := newProvisioningManager(t, lvmMock, minioMock)

	job := &Job{ID: "job-createfail", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := m.ProvisionVolume(context.Background(), job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create volume")

	calls := lvmMock.snapshot()
	assert.Equal(t, 1, calls.create)
	assert.Equal(t, 0, calls.populate, "populate must not run after a failed create")
	assert.Equal(t, 0, calls.delete, "a volume that was never created must not be deleted")
}

// A rollback that itself fails must be reported, not swallowed: the volume is
// then leaked and the operator has to reclaim it by hand.
func TestProvisionVolume_ReportsFailedRollback(t *testing.T) {
	lvmMock := &fakeLVM{
		populateErr: errors.New("populate failed"),
		deleteErr:   errors.New("volume is mapped"),
	}
	minioMock := &fakeMinio{content: []byte("image-bytes")}
	m := newProvisioningManager(t, lvmMock, minioMock)

	job := &Job{ID: "job-rollbackfail", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := m.ProvisionVolume(context.Background(), job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "populate failed")

	calls := lvmMock.snapshot()
	assert.Equal(t, 1, calls.delete, "the reclaim must still be attempted")
}

// A failure to acquire the image happens before any volume exists.
func TestProvisionVolume_NoVolumeWorkWhenImageFetchFails(t *testing.T) {
	lvmMock := &fakeLVM{}
	minioMock := &fakeMinio{err: errors.New("object not found")}
	m := newProvisioningManager(t, lvmMock, minioMock)

	job := &Job{ID: "job-noimage", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := m.ProvisionVolume(context.Background(), job)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get image")

	calls := lvmMock.snapshot()
	assert.Zero(t, calls.create, "no volume may be created without an image")
	assert.Zero(t, calls.populate)
	assert.Zero(t, calls.delete)
}

// -- dependency guards -------------------------------------------------

// Every dependency is required; a partially wired manager must refuse rather
// than nil-deref part way through a provision.
func TestProvisionVolume_MissingDependenciesAreRefused(t *testing.T) {
	lvmMock := &fakeLVM{}
	minioMock := &fakeMinio{content: []byte("x")}
	full := newProvisioningManager(t, lvmMock, minioMock)

	cases := []struct {
		name string
		m    *Manager
	}{
		{"no minio", &Manager{lvmManager: lvmMock, libvirtPool: &mockLibvirtPool{}, store: newTestStore(t)}},
		{"no lvm", &Manager{minioClient: minioMock, libvirtPool: &mockLibvirtPool{}, store: newTestStore(t)}},
		{"no pool", &Manager{minioClient: minioMock, lvmManager: lvmMock, store: newTestStore(t)}},
		{"no store", &Manager{minioClient: minioMock, lvmManager: lvmMock, libvirtPool: &mockLibvirtPool{}}},
		{"nothing", &Manager{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := &Job{ID: "job-deps", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
			err := tc.m.ProvisionVolume(context.Background(), job)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "dependencies not initialized")
		})
	}

	// Sanity: the fully wired manager must not take the guard path.
	job := &Job{ID: "job-ok", Request: provisionReq(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := full.ProvisionVolume(context.Background(), job); err != nil {
		assert.NotContains(t, err.Error(), "dependencies not initialized")
	}
}

func writeFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}
