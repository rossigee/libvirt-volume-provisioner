package lvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/stretchr/testify/require"
)

// fakeResp is the canned result for one LVM tool invocation.
type fakeResp struct {
	stdout string
	stderr string
	code   int
}

// fakeLVM substitutes lvmCmd so the parsing and validation logic can be driven
// with LVM output that does not require a real volume group.
type fakeLVM struct {
	mu        sync.Mutex
	calls     []string
	responses map[string]fakeResp
	matched   []fakeMatch
}

// fakeMatch keys a response off part of the command line, for tools invoked more
// than once with different arguments - vgs is asked for extent counts and then
// for free extents.
type fakeMatch struct {
	contains string
	resp     fakeResp
}

func newFakeLVM() *fakeLVM {
	return &fakeLVM{responses: map[string]fakeResp{}}
}

// on sets the canned response for a tool.
func (f *fakeLVM) on(tool string, r fakeResp) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[tool] = r
}

// onArgs matches part of the command line, and takes precedence over on().
func (f *fakeLVM) onArgs(contains string, r fakeResp) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.matched = append(f.matched, fakeMatch{contains: contains, resp: r})
}

// fail is shorthand for a tool exiting non-zero with a message on stderr.
func (f *fakeLVM) fail(tool, msg string) {
	f.on(tool, fakeResp{stderr: msg, code: 1})
}

func (f *fakeLVM) record(name string, args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
}

// callsTo returns the recorded command lines for a tool.
func (f *fakeLVM) callsTo(tool string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, tool+" ") || c == tool {
			out = append(out, c)
		}
	}
	return out
}

// lookup resolves the canned response for one invocation.
func (f *fakeLVM) lookup(name string, args []string) fakeResp {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))

	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.matched) - 1; i >= 0; i-- {
		if strings.Contains(line, f.matched[i].contains) {
			return f.matched[i].resp
		}
	}
	return f.responses[name]
}

// shQuote single-quotes s for safe interpolation into a sh -c script.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// install points lvmCmd at the fake for the duration of the test.
func (f *fakeLVM) install(t *testing.T) {
	t.Helper()
	original := lvmCmd
	t.Cleanup(func() { lvmCmd = original })

	lvmCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		f.record(name, args)
		resp := f.lookup(name, args)

		script := fmt.Sprintf("printf %%s %s; printf %%s %s >&2; exit %d",
			shQuote(resp.stdout), shQuote(resp.stderr), resp.code)

		cmd := exec.CommandContext(ctx, "sh", "-c", script)
		// Mirror the real helper so locale-sensitive output stays ASCII.
		cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
		return cmd
	}
}

// toolOnlyPath builds a PATH containing only sh plus the named tools, so
// exec.LookPath can be made to fail for a specific binary. sh must remain
// reachable because the fake runs through it.
func toolOnlyPath(t *testing.T, tools ...string) string {
	t.Helper()
	dir := t.TempDir()

	link := func(name string) {
		src, err := exec.LookPath(name)
		require.NoError(t, err, "%s must exist to build the fake PATH", name)
		require.NoError(t, os.Symlink(src, filepath.Join(dir, name)))
	}

	link("sh")
	for _, tool := range tools {
		link(tool)
	}
	return dir
}

// activeLV is a VolumeInfo line as `lvs --units b -o lv_name,lv_size,lv_attr` emits it.
func activeLV(name string, sizeBytes int64) string {
	return fmt.Sprintf("%s %dB owi-a-----\n", name, sizeBytes)
}

func fakeLVMCfg(vg string) config.LVMConfig {
	return config.LVMConfig{
		VolumeGroup:    vg,
		RetryAttempts:  2,
		RetryBackoffMS: []int{100, 1000},
	}
}

// -- GetVolumeInfo -------------------------------------------------------

func TestGetVolumeInfo_ParsesLvsOutput(t *testing.T) {
	f := newFakeLVM()
	f.on("lvs", fakeResp{stdout: activeLV("runner-v4", 10737418240)})
	f.install(t)

	m := &Manager{vgName: "vg0"}

	info, err := m.GetVolumeInfo(context.Background(), "runner-v4")
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "runner-v4", info.Name)
	require.Equal(t, int64(10737418240), info.SizeBytes)
	require.Equal(t, "owi-a-----", info.Attributes)

	// The volume must be probed with lvs before the sized query, and the sized
	// query must ask for bytes so the "B" suffix can be stripped.
	require.Len(t, f.callsTo("lvs"), 2)
	require.Contains(t, f.callsTo("lvs")[1], "--units b")
	require.Contains(t, f.callsTo("lvs")[1], "lv_name,lv_size,lv_attr")
	require.Contains(t, f.callsTo("lvs")[1], "vg0/runner-v4")
}

func TestGetVolumeInfo_TruncatedOutputIsRejected(t *testing.T) {
	// Two fields where three are expected: a wrapped or truncated lvs row must
	// fail loudly rather than yield a zero-size volume.
	for _, out := range []string{"", "\n", "runner-v4\n", "runner-v4 10737418240B\n"} {
		f := newFakeLVM()
		f.on("lvs", fakeResp{stdout: out})
		f.install(t)

		m := &Manager{vgName: "vg0"}
		_, err := m.GetVolumeInfo(context.Background(), "runner-v4")
		require.Error(t, err, "output %q", out)
		require.Contains(t, err.Error(), "unexpected lvs output format")
	}
}

func TestGetVolumeInfo_UnparseableSizeIsRejected(t *testing.T) {
	f := newFakeLVM()
	f.on("lvs", fakeResp{stdout: "runner-v4 notanumberB owi-a-----\n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}

	_, err := m.GetVolumeInfo(context.Background(), "runner-v4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse volume size")
}

// -- validateExistingVolume ----------------------------------------------

func TestValidateExistingVolume_SizeTolerance(t *testing.T) {
	const oneGB = 1000 * 1000 * 1000

	cases := []struct {
		name       string
		sizeBytes  int64
		wantErrStr string
	}{
		{"comfortably larger", 10 * oneGB, ""},
		// 5% variance is allowed for filesystem overhead.
		{"exactly at the 5% floor", oneGB - oneGB/20, ""},
		{"just under the 5% floor", oneGB - oneGB/20 - 1, "too small"},
		{"much too small", oneGB / 2, "too small"},
		{"zero", 0, "too small"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLVM()
			f.on("lvs", fakeResp{stdout: activeLV("runner-v4", tc.sizeBytes)})
			f.install(t)

			m := &Manager{vgName: "vg0"}
			err := m.validateExistingVolume(context.Background(), "runner-v4", 1)

			if tc.wantErrStr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErrStr)
		})
	}
}

// -- ListVolumes ---------------------------------------------------------

func TestListVolumes_ParsesMultipleRows(t *testing.T) {
	f := newFakeLVM()
	f.on("lvs", fakeResp{stdout: "runner-v4\nrunner-v5\n\n  runner-v6  \n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}

	volumes, err := m.ListVolumes()
	require.NoError(t, err)
	require.Equal(t, []string{"runner-v4", "runner-v5", "runner-v6"}, volumes)
}

func TestListVolumes_EmptyGroup(t *testing.T) {
	f := newFakeLVM()
	f.on("lvs", fakeResp{stdout: "\n  \n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}

	volumes, err := m.ListVolumes()
	require.NoError(t, err)
	require.Empty(t, volumes)
}

// -- validateDeviceBeforeConversion --------------------------------------

func TestValidateDeviceBeforeConversion_RejectsShortAttributes(t *testing.T) {
	f := newFakeLVM()
	f.on("lvs", fakeResp{stdout: "runner-v4 10737418240B owi\n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}

	// Position 4 of the attribute string carries activation state; a shorter
	// string means lvs output is not what we expect.
	err := m.validateDeviceBeforeConversion(
		context.Background(), "/dev/vg0/runner-v4", "runner-v4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid LV attributes format")
}

func TestValidateDeviceBeforeConversion_RejectsInactiveVolume(t *testing.T) {
	// '-' in position 4 means inactive; anything else that is not 'a' is refused.
	f := newFakeLVM()
	f.on("lvs", fakeResp{stdout: "runner-v4 10737418240B owi-n-----\n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}

	err := m.validateDeviceBeforeConversion(
		context.Background(), "/dev/vg0/runner-v4", "runner-v4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not active or available")
}

func TestValidateDeviceBeforeConversion_LvdisplayFailureStopsEarly(t *testing.T) {
	f := newFakeLVM()
	f.fail("lvdisplay", "Volume group vg0 not found")
	f.install(t)

	m := &Manager{vgName: "vg0"}

	err := m.validateDeviceBeforeConversion(
		context.Background(), "/dev/vg0/runner-v4", "runner-v4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist or is not accessible")
	require.Contains(t, err.Error(), "vg0/runner-v4")

	// Nothing beyond the existence check should have run.
	require.Empty(t, f.callsTo("lvs"))
}

// -- createVolumeOnce ----------------------------------------------------

func TestCreateVolumeOnce_InsufficientExtents(t *testing.T) {
	// 1 GB needs 256 extents; only 10 are free.
	f := newFakeLVM()
	f.onArgs("pv_count,extent_count", fakeResp{stdout: "1 1000\n"})
	f.onArgs("free_count", fakeResp{stdout: "10\n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}
	err := m.createVolumeOnce(context.Background(), "runner-v4", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not enough free extents")
	require.Contains(t, err.Error(), "need 256")
	require.Contains(t, err.Error(), "have 10")

	// Allocation must not be attempted once the extent check fails.
	require.Empty(t, f.callsTo("lvcreate"))
}

func TestCreateVolumeOnce_UnparseableVgsOutput(t *testing.T) {
	f := newFakeLVM()
	f.on("vgs", fakeResp{stdout: "not a number at all\n"})
	f.install(t)

	m := &Manager{vgName: "vg0"}
	err := m.createVolumeOnce(context.Background(), "runner-v4", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse vgs output")
}

func TestCreateVolumeOnce_LvcreateFailure(t *testing.T) {
	f := newFakeLVM()
	f.onArgs("pv_count,extent_count", fakeResp{stdout: "1 1000\n"})
	f.onArgs("free_count", fakeResp{stdout: "1000\n"})
	f.fail("lvcreate", "Volume group vg0 not found")
	f.install(t)

	m := &Manager{vgName: "vg0"}
	err := m.createVolumeOnce(context.Background(), "runner-v4", 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create LVM volume")
}

// -- NewManager ----------------------------------------------------------

func TestNewManager_SuccessConvertsRetryBackoff(t *testing.T) {
	f := newFakeLVM()
	f.on("vgs", fakeResp{})
	f.install(t)

	m, err := NewManager(config.LVMConfig{
		VolumeGroup:    "vg0",
		RetryAttempts:  5,
		RetryBackoffMS: []int{250, 2500, 25000},
	})
	require.NoError(t, err)
	require.NotNil(t, m)

	require.Equal(t, "vg0", m.vgName)
	require.Equal(t, 5, m.retryConfig.MaxAttempts)
	require.Len(t, m.retryConfig.Delays, 3)
}

func TestNewManager_VgsFailure(t *testing.T) {
	f := newFakeLVM()
	f.fail("vgs", "failed to find volume group vg-nope")
	f.install(t)

	m, err := NewManager(fakeLVMCfg("vg-nope"))
	require.Error(t, err)
	require.Nil(t, m)
	require.Contains(t, err.Error(), "does not exist or is not accessible")
}

func TestNewManager_MissingLvcreate(t *testing.T) {
	f := newFakeLVM()
	f.on("vgs", fakeResp{})
	f.install(t)

	// sh stays on the PATH because the fake runs through it; lvcreate does not.
	t.Setenv("PATH", toolOnlyPath(t))

	m, err := NewManager(fakeLVMCfg("vg0"))
	require.Error(t, err)
	require.Nil(t, m)
	require.Contains(t, err.Error(), "lvcreate command not found")
}

func TestNewManager_MissingQemuImg(t *testing.T) {
	f := newFakeLVM()
	f.on("vgs", fakeResp{})
	f.install(t)

	// Keep lvcreate on the PATH but not qemu-img, to reach the second check.
	t.Setenv("PATH", toolOnlyPath(t, "lvcreate"))

	m, err := NewManager(fakeLVMCfg("vg0"))
	require.Error(t, err)
	require.Nil(t, m)
	require.Contains(t, err.Error(), "qemu-img command not found")
}

// -- the default helper still forces the C locale ------------------------

func TestLvmCmd_DefaultForcesCLocale(t *testing.T) {
	// The seam must not weaken the locale guarantee that the production helper
	// exists to provide.
	cmd := lvmCmd(context.Background(), "vgs", "vg0")
	require.NotNil(t, cmd)

	var hasLC, hasLang bool
	for _, kv := range cmd.Env {
		if kv == "LC_ALL=C" {
			hasLC = true
		}
		if kv == "LANG=C" {
			hasLang = true
		}
	}
	require.True(t, hasLC, "LC_ALL=C must be set on every LVM child process")
	require.True(t, hasLang, "LANG=C must be set on every LVM child process")
}
