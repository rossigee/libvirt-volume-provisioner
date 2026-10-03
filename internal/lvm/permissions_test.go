package lvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runningAsRoot gates the assertions that need CAP_CHOWN. setVolumePermissions
// chowns the block device to root:disk, which an unprivileged test process
// cannot do.
func runningAsRoot() bool {
	return os.Geteuid() == 0
}

func TestNewManager_RejectsPathSeparatorsInVolumeGroup(t *testing.T) {
	// The guard runs before any LVM command, so these are asserted without a
	// real volume group.
	for _, vg := range []string{
		"vg/data",
		"vg\\data",
		"/",
		"\\",
		"../escape",
		`..\escape`,
	} {
		t.Run(vg, func(t *testing.T) {
			m, err := NewManager(testLVMCfg(vg))
			require.Error(t, err)
			assert.Nil(t, m)
			assert.Contains(t, err.Error(), "must not contain path separators")
		})
	}
}

func TestNewManager_NonExistentVolumeGroup(t *testing.T) {
	if _, err := os.Stat("/dev/"); err != nil {
		t.Skip("no /dev")
	}

	m, err := NewManager(testLVMCfg("definitely-not-a-real-vg-xyz"))
	require.Error(t, err)
	assert.Nil(t, m)
	assert.Contains(t, err.Error(), "does not exist or is not accessible")
}

func TestSetVolumePermissions_NonExistentDevice(t *testing.T) {
	manager := &Manager{vgName: "testvg"}
	device := filepath.Join(t.TempDir(), "absent-device")

	err := manager.setVolumePermissions(device, "vol-absent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set group on volume vol-absent")
}

// setVolumePermissions chowns to uid 0 / gid 6. Without CAP_CHOWN that chown is
// the step that fails, on a path that genuinely exists - which is the case this
// asserts, as opposed to the missing-file case above.
func TestSetVolumePermissions_ChownFailsWithoutPrivileges(t *testing.T) {
	if runningAsRoot() {
		t.Skip("running as root: chown to 0:6 succeeds, cannot exercise the failure")
	}

	device := filepath.Join(t.TempDir(), "device")
	require.NoError(t, os.WriteFile(device, nil, 0o600))

	manager := &Manager{vgName: "testvg"}
	err := manager.setVolumePermissions(device, "vol-chown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to set group on volume vol-chown")
}

func TestSetVolumePermissions_Success(t *testing.T) {
	if !runningAsRoot() {
		t.Skip("needs root to chown the device to root:disk")
	}

	device := filepath.Join(t.TempDir(), "device")
	require.NoError(t, os.WriteFile(device, nil, 0o600))

	manager := &Manager{vgName: "testvg"}
	require.NoError(t, manager.setVolumePermissions(device, "vol-ok"))

	info, err := os.Stat(device)
	require.NoError(t, err)

	stat, ok := info.Sys().(interface{ Gid() uint32 })
	require.True(t, ok, "expected stat to expose Gid")
	assert.Equal(t, uint32(6), stat.Gid(), "block device must be group disk for libvirt/qemu")
	assert.Equal(t, os.FileMode(0o660), info.Mode().Perm())
}

// Deletion is idempotent: a rollback whose volume is already gone must succeed,
// because the point of the rollback is that the volume ends up absent.
func TestDeleteVolume_AbsentVolumeSucceeds(t *testing.T) {
	manager := &Manager{vgName: "definitely-not-a-real-vg-xyz"}

	require.NoError(t, manager.DeleteVolume(context.Background(), "never-existed"))
}

// Name validation must reject traversal before any LVM command is built, so a
// hostile name cannot reach outside /dev/<vg>.
func TestDeleteVolume_RejectsInvalidNameBeforeExec(t *testing.T) {
	manager := &Manager{vgName: "testvg"}

	// The validator checks separators before parent references, so a name with
	// both is reported as a separator violation.
	cases := []struct {
		name    string
		wantErr string
	}{
		{"../escape", "must not contain path separators"},
		{"a/b", "must not contain path separators"},
		{`a\b`, "must not contain path separators"},
		{"a..b", "parent references"},
		{".", "parent references"},
		{"..", "parent references"},
		{"with space", "disallowed character"},
		{"quote\"name", "disallowed character"},
		{"semi;colon", "disallowed character"},
		{"", "must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := manager.DeleteVolume(context.Background(), tc.name)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestCreateVolume_RejectsInvalidNameBeforeExec(t *testing.T) {
	manager := &Manager{vgName: "testvg"}

	for _, name := range []string{"../escape", "a/b", "with space", ""} {
		t.Run(name, func(t *testing.T) {
			err := manager.CreateVolume(context.Background(), name, 1)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "failed to create LVM volume",
				"validation must reject the name before any LVM command runs")
		})
	}
}

func TestGetVolumeInfo_NonExistentVolume(t *testing.T) {
	manager := &Manager{vgName: "definitely-not-a-real-vg-xyz"}

	info, err := manager.GetVolumeInfo(context.Background(), "no-such-volume")
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestValidateExistingVolume_PropagatesLookupFailure(t *testing.T) {
	manager := &Manager{vgName: "definitely-not-a-real-vg-xyz"}

	err := manager.validateExistingVolume(context.Background(), "no-such-volume", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get volume info")
}

func TestListVolumes_NonExistentVolumeGroup(t *testing.T) {
	manager := &Manager{vgName: "definitely-not-a-real-vg-xyz"}

	volumes, err := manager.ListVolumes()
	require.Error(t, err)
	assert.Nil(t, volumes)
	assert.Contains(t, err.Error(), "failed to list volumes")
}

// The pre-conversion guard must reject a volume that is not really there, which
// is what stops qemu-img reporting "Cannot grow device files".
func TestValidateDeviceBeforeConversion_MissingVolume(t *testing.T) {
	manager := &Manager{vgName: "definitely-not-a-real-vg-xyz"}

	err := manager.validateDeviceBeforeConversion(
		context.Background(), "/dev/definitely-not-a-real-vg-xyz/absent", "absent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist or is not accessible")
}

// PopulateVolume must refuse to write to a device that failed validation.
func TestPopulateVolume_MissingVolumeIsRejected(t *testing.T) {
	manager := &Manager{vgName: "definitely-not-a-real-vg-xyz"}

	source := filepath.Join(t.TempDir(), "source.img")
	require.NoError(t, os.WriteFile(source, []byte("not a real qcow2"), 0o600))

	err := manager.PopulateVolume(
		context.Background(), source, "absent-volume", "qcow2",
		&MockProgressUpdater{}, nil, "")
	require.Error(t, err)
}
