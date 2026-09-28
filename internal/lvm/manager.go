// Package lvm provides functionality for managing LVM (Logical Volume Manager)
// volumes including creation, conversion, and removal operations.
package lvm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	qcow2reader "github.com/lima-vm/go-qcow2reader"
	"github.com/lima-vm/go-qcow2reader/image/qcow2"
	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/rossigee/libvirt-volume-provisioner/internal/retry"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/rossigee/libvirt-volume-provisioner/internal/timing"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// v0.9: sudoCmd removed - using direct exec.Command

// lvmCmd builds an exec.Cmd for an LVM tool with the C locale forced.
//
// LVM tools localise their output. Under a locale such as th_TH, vgs emits
// fullwidth digits, and fmt.Sscanf's %d accepts only ASCII digits, so parsing
// fails with "expected integer" and every volume creation aborts. The same
// class of defect produced the BE 2569 build timestamps in the image builds.
// Forcing LC_ALL=C on the child process makes the numeric output stable
// regardless of the host's configured locale, and is how these tools are
// intended to be scripted.
func lvmCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	// #nosec G204 -- `name` is never caller-supplied. Every call site in this
	// package passes a string literal for the tool ("vgs", "lvs", "lvcreate",
	// "lvremove"); only the arguments are dynamic, and they are passed as
	// separate argv entries rather than through a shell, so they cannot be
	// re-parsed as syntax. Centralising exec here is what forces LC_ALL=C onto
	// every LVM child process, which is the point of this helper.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	return cmd
}

// normalizeNumericOutput reduces LVM tool output to a form fmt.Sscanf's %d can
// read, so a volume can be created regardless of the host's locale.
//
// Two things are handled:
//
//   - Fullwidth digits. Under a locale such as th_TH, LVM emits U+FF10..U+FF19
//     rather than ASCII 0-9, and %d rejects them with "expected integer". They
//     are folded to their ASCII equivalents.
//   - Column wrapping. Without --nosuffix, LVM wraps a long row at the terminal
//     width, so a row arrives split across two lines and %d fails with
//     "newline in input does not match format". Whitespace, including the line
//     break, becomes a single field separator.
//
// Digit grouping is deliberately not handled. vgs is asked for raw count fields
// (pv_count, extent_count, free_count) with no --units, so LVM emits them
// ungrouped. Supporting grouping would be ambiguous: "102,399" and a row
// wrapped as "102" / "399" are indistinguishable from the text alone, and
// guessing wrong yields a wrong extent count with no error, which is worse than
// a parse failure.
func normalizeNumericOutput(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '\uFF10' && r <= '\uFF19': // fullwidth digit
			b.WriteRune('0' + (r - '\uFF10'))
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ProgressUpdater interface for updating job progress
type ProgressUpdater interface {
	UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64)
}

// Manager handles LVM operations
type Manager struct {
	vgName      string
	retryConfig retry.Config
}

// NewManager creates a new LVM manager from the provided configuration.
func NewManager(cfg config.LVMConfig) (*Manager, error) {
	if strings.ContainsAny(cfg.VolumeGroup, "/\\") {
		return nil, fmt.Errorf("invalid volume group name %q: must not contain path separators", cfg.VolumeGroup)
	}

	cmd := lvmCmd(context.Background(), "vgs", cfg.VolumeGroup)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("volume group %q does not exist or is not accessible: %w", cfg.VolumeGroup, err)
	}

	if _, err := exec.LookPath("lvcreate"); err != nil {
		return nil, fmt.Errorf("lvcreate command not found: %w", err)
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return nil, fmt.Errorf("qemu-img command not found: %w", err)
	}

	delays := make([]time.Duration, len(cfg.RetryBackoffMS))
	for i, ms := range cfg.RetryBackoffMS {
		delays[i] = time.Duration(ms) * time.Millisecond
	}

	return &Manager{
		vgName: cfg.VolumeGroup,
		retryConfig: retry.Config{
			MaxAttempts: cfg.RetryAttempts,
			Delays:      delays,
		},
	}, nil
}

// CreateVolume creates a new LVM volume with exponential backoff retry
// If volume exists, validates it matches requirements and reuses if compatible
func (m *Manager) CreateVolume(ctx context.Context, volumeName string, sizeGB int) error {
	// Start span for LVM volume creation
	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "CreateVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.Int("lvm.size_gb", sizeGB),
			attribute.String("lvm.vg_name", m.vgName)))
	defer span.End()

	// Check if volume already exists
	if m.volumeExists(ctx, volumeName) {
		// Validate existing volume
		if err := m.validateExistingVolume(ctx, volumeName, sizeGB); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("existing volume %s is incompatible: %w", volumeName, err)
		}
		// Set permissions so libvirt/qemu can access the block device
		devicePath := fmt.Sprintf("/dev/%s/%s", m.vgName, volumeName)
		if err := m.setVolumePermissions(devicePath, volumeName); err != nil {
			return err
		}
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"size_gb":     sizeGB,
		}).Info("Reusing existing compatible volume")
		span.SetStatus(codes.Ok, "reused existing compatible volume")
		return nil
	}

	// Create new volume
	err := retry.WithRetry(ctx, m.retryConfig, func() error {
		return m.createVolumeOnce(ctx, volumeName, sizeGB)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to create volume %s after retries: %w", volumeName, err)
	}
	span.SetStatus(codes.Ok, "volume created successfully")
	return nil
}

// createVolumeOnce performs a single LVM volume creation attempt
func (m *Manager) createVolumeOnce(ctx context.Context, volumeName string, sizeGB int) error {
	// Get the number of physical extents to allocate for fully-allocated volume
	cmd := lvmCmd(ctx, "vgs", "--noheadings", "-o", "pv_count,extent_count", m.vgName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get volume group info: %w, output: %s", err, string(output))
	}

	var pvCount, extentCount int
	if _, err := fmt.Sscanf(normalizeNumericOutput(string(output)), "%d %d", &pvCount, &extentCount); err != nil {
		return fmt.Errorf("failed to parse vgs output: %w", err)
	}

	// Calculate extents based on requested sizeGB
	// Each extent is 4MB by default on this system
	extentsPerGB := 256 // 4MB * 256 = 1GB
	extents := sizeGB * extentsPerGB

	// Verify we have enough extents available
	cmd = lvmCmd(ctx, "vgs", "--noheadings", "-o", "free_count", m.vgName)
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to get free extents: %w, output: %s", err, string(output))
	}
	var freeExtents int
	if _, err := fmt.Sscanf(normalizeNumericOutput(string(output)), "%d", &freeExtents); err != nil {
		return fmt.Errorf("failed to parse free extent count: %w", err)
	}
	if extents > freeExtents {
		return fmt.Errorf("not enough free extents: need %d, have %d", extents, freeExtents)
	}

	// Create fully-allocated LVM volume using extents
	cmd = lvmCmd(ctx, "lvcreate",
		"-l", fmt.Sprintf("%d", extents),
		"--type", "linear",
		"-n", volumeName, m.vgName)
	output, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create LVM volume: %w, output: %s", err, string(output))
	}

	// Set permissions so libvirt/qemu can access the block device
	devicePath := fmt.Sprintf("/dev/%s/%s", m.vgName, volumeName)
	if err := m.setVolumePermissions(devicePath, volumeName); err != nil {
		return err
	}

	return nil
}

func (m *Manager) setVolumePermissions(devicePath, volumeName string) error {
	if err := os.Chown(devicePath, 0, 6); err != nil {
		return fmt.Errorf("failed to set group on volume %s: %w", volumeName, err)
	}
	// devicePath is an LVM block device, not a file containing secrets, and the
	// Chown above puts it in root:disk (gid 6) so the libvirt/qemu process can
	// open it. Restricting it to 0600 as G302 suggests would leave the volume
	// unreadable to the VM that was just provisioned.
	// #nosec G302
	if err := os.Chmod(devicePath, 0660); err != nil {
		return fmt.Errorf("failed to set permissions on volume %s: %w", volumeName, err)
	}
	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"device_path": devicePath,
	}).Info("Set permissions on LVM volume")
	return nil
}

// validateDeviceBeforeConversion performs comprehensive checks to ensure the LVM
// volume is properly created, active, and accessible before attempting qemu-img conversion.
// This prevents "Cannot grow device files" errors by catching missing/inactive volumes early.
func (m *Manager) validateDeviceBeforeConversion(ctx context.Context, devicePath, volumeName string) error {
	// 1. Check if LV exists in LVM metadata
	fullPath := fmt.Sprintf("%s/%s", m.vgName, volumeName)
	lvdisplayCmd := lvmCmd(ctx, "lvdisplay", fullPath)
	if err := lvdisplayCmd.Run(); err != nil {
		return fmt.Errorf("LVM logical volume does not exist or is not accessible: %s: %w", fullPath, err)
	}

	// 2. Get volume info to verify it's active and has correct attributes
	info, err := m.GetVolumeInfo(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("failed to get volume info for validation: %w", err)
	}

	// Check if volume is active (attribute[4] should be 'a' for active or '-' for inactive but available)
	// Attribute string format: "owi-a-----" where position 4 is the allocation/activation status
	if len(info.Attributes) < 5 {
		return fmt.Errorf("invalid LV attributes format: %q", info.Attributes)
	}
	if info.Attributes[4] != 'a' && info.Attributes[4] != '-' {
		return fmt.Errorf("LVM volume is not active or available (status: %c in attributes: %s)",
			info.Attributes[4], info.Attributes)
	}

	// 3. Verify the block device file actually exists and is a block device
	fi, err := os.Stat(devicePath)
	if err != nil {
		return fmt.Errorf("device path does not exist: %s: %w", devicePath, err)
	}
	if fi.Mode()&os.ModeDevice == 0 {
		return fmt.Errorf("device path exists but is not a block device: %s (mode: %v)", devicePath, fi.Mode())
	}

	// 4. Verify the device is readable and writable
	if _, err := os.Open(devicePath); err != nil {
		return fmt.Errorf("device is not readable: %s: %w", devicePath, err)
	}

	// 5. Verify the device can be written to (attempt to get current size)
	// This catches issues where the device exists but isn't properly initialized
	cmd := exec.CommandContext(ctx, "blockdev", "--getsize64", devicePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("device size query failed (device may not be properly initialized): %s: %w (output: %s)",
			devicePath, err, string(output))
	}

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"device_path": devicePath,
		"lv_path":     fullPath,
		"lv_size":     info.SizeBytes,
		"lv_attr":     info.Attributes,
		"device_size": string(bytes.TrimSpace(output)),
	}).Info("Device validation passed, proceeding with conversion")

	return nil
}

// PopulateVolume populates an LVM volume with image data with exponential backoff retry
func (m *Manager) PopulateVolume(
	ctx context.Context,
	imagePath, volumeName, imageType string,
	updater ProgressUpdater,
	store *storage.Store,
	jobID string,
) error {
	// Start span for LVM volume population
	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "PopulateVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.String("lvm.image_path", imagePath),
			attribute.String("lvm.image_type", imageType)))
	defer span.End()

	// Wrap with retry logic
	err := retry.WithRetry(ctx, m.retryConfig, func() error {
		return m.populateVolumeOnce(ctx, imagePath, volumeName, imageType, updater, store, jobID)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to populate volume %s after retries: %w", volumeName, err)
	}
	span.SetStatus(codes.Ok, "volume populated successfully")
	return nil
}

// qcow2ConvertArgs returns the qemu-img arguments to convert a qcow2 image
// to raw and write it directly to devicePath. Extracted so tests can verify
// the correct output target is used without running the command.
//
// -T none / -t none select O_DIRECT for the source read and target write
// respectively. Without them qemu-img's reads and writes go through the
// host page cache, which cgroup v2 memory.max accounts against this
// service's memory cgroup exactly like RSS. A single ~20GB conversion is
// enough to fill a 512MB limit with clean page cache alone (observed:
// memory.stat anon=8MB, file=510MB), triggering OOM-kill/reclaim mid-write
// and leaving a truncated or corrupted volume even though the daemon
// itself never leaks heap.
func qcow2ConvertArgs(imagePath, devicePath string) []string {
	return []string{"convert", "-f", "qcow2", "-O", "raw", "-T", "none", "-t", "none", imagePath, devicePath}
}

// populateVolumeOnce performs a single volume population attempt
func (m *Manager) populateVolumeOnce(
	ctx context.Context,
	imagePath, volumeName, imageType string,
	updater ProgressUpdater, store *storage.Store, jobID string,
) error {
	// Get the device path for the LVM volume
	devicePath := fmt.Sprintf("/dev/%s/%s", m.vgName, volumeName)

	// Comprehensive validation before attempting conversion
	// This catches issues early (missing LV, inactive status, device not accessible)
	// preventing "Cannot grow device files" errors during qemu-img conversion
	if err := m.validateDeviceBeforeConversion(ctx, devicePath, volumeName); err != nil {
		return fmt.Errorf("device validation failed before conversion: %w", err)
	}

	// Check if volume already has content by looking for filesystem
	blkidCmd := exec.CommandContext(ctx, "blkid", "-o", "value", "-s", "TYPE", devicePath)
	blkidOutput, blkidErr := blkidCmd.Output()

	// If blkid finds a filesystem, the volume has content
	if blkidErr == nil && strings.TrimSpace(string(blkidOutput)) != "" {
		filesystemType := strings.TrimSpace(string(blkidOutput))
		logrus.WithFields(logrus.Fields{
			"volume_name":     volumeName,
			"device_path":     devicePath,
			"filesystem_type": filesystemType,
		}).Info("Volume already has filesystem, skipping population")
		return nil
	}

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"device_path": devicePath,
	}).Info("Volume has no filesystem, proceeding with population")

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"device_path": devicePath,
		"image_path":  imagePath,
		"image_type":  imageType,
	}).Info("Starting volume population")

	// Convert image format if needed and copy to LVM volume
	// For qcow2 images, we use streaming conversion with small buffer to avoid OOM
	var cmd *exec.Cmd
	var imageSize int64
	switch imageType {
	case "qcow2":
		// Read virtual size from qcow2 header — this is the uncompressed bytes that
		// will be written to the block device, not the compressed on-disk file size.
		if err := func() error {
			imgFile, err := os.Open(imagePath)
			if err != nil {
				return fmt.Errorf("failed to open image file %s: %w", imagePath, err)
			}
			defer func() {
				if err := imgFile.Close(); err != nil {
					logrus.WithError(err).Warn("failed to close image file after header read")
				}
			}()
			qimg, err := qcow2reader.OpenWithType(imgFile, qcow2.Type)
			if err != nil {
				return fmt.Errorf("failed to read qcow2 header from %s: %w", imagePath, err)
			}
			defer func() {
				if err := qimg.Close(); err != nil {
					logrus.WithError(err).Warn("failed to close qcow2 image after header read")
				}
			}()
			imageSize = qimg.Size()
			return nil
		}(); err != nil {
			return err
		}

		// Write directly to the device path. Avoid using '-' (stdout) as the output
		// target: in QEMU ≥10.1.0, stdout-based conversion is broken — '-p' progress
		// goes to stdout corrupting the stream, and without '-p' qemu-img writes
		// 0 bytes. Writing directly to the block device path avoids both issues.
		cmd = exec.CommandContext(ctx, "qemu-img", qcow2ConvertArgs(imagePath, devicePath)...)
	case "raw":
		// Direct copy for raw images
		cmd = exec.CommandContext(ctx, "dd", "if="+imagePath, "of="+devicePath, "bs=4M", "status=progress", "conv=fdatasync")
		rawStat, statErr := os.Stat(imagePath)
		if statErr != nil {
			return fmt.Errorf("failed to stat image file: %w", statErr)
		}
		imageSize = rawStat.Size()
	default:
		return fmt.Errorf("unsupported image type: %s", imageType)
	}

	// Execute conversion with time-based progress tracking.
	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"command":     strings.Join(cmd.Args, " "),
	}).Info("Executing volume population command")

	startTime := time.Now()

	// Capture stderr for error reporting; for non-qcow2 types also capture stdout.
	var outBuf bytes.Buffer
	if cmd.Stdout == nil {
		cmd.Stdout = &outBuf
	}
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	// Use stored convert rate to estimate duration for incremental progress ticks.
	var estimatedSeconds float64

	// Guard against zero/empty image - no progress to report if there's no data
	if imageSize > 0 {
		estimatedSeconds = float64(imageSize) / timing.DefaultConvertRate // default estimate
		if store != nil {
			if rate := store.GetAverageRate(ctx, "convert", timing.DefaultConvertRate); rate > 0 {
				estimatedSeconds = float64(imageSize) / rate // use historical rate if available
			}
		}
	}
	// If imageSize is 0 or estimate is somehow invalid, use minimum 1 second
	if estimatedSeconds <= 0 {
		estimatedSeconds = 1
	}

	logrus.WithFields(logrus.Fields{
		"volume_name":   volumeName,
		"image_size":    imageSize,
		"estimated_sec": estimatedSeconds,
		"default_rate":  timing.DefaultConvertRate,
	}).Info("Starting volume conversion with progress tracking")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var executionErr error
loop:
	for {
		select {
		case executionErr = <-waitDone:
			break loop
		case <-ticker.C:
			if updater != nil {
				elapsed := time.Since(startTime).Seconds()
				pct := min(elapsed/estimatedSeconds*100, 99.0)
				processed := int64(float64(imageSize) * pct / 100)
				// Log progress at INFO level every 5 seconds to diagnose 0% issue
				if int(elapsed)%5 == 0 {
					logrus.WithFields(logrus.Fields{
						"elapsed_sec":   elapsed,
						"estimated_sec": estimatedSeconds,
						"percent":       pct,
						"image_size":    imageSize,
					}).Info("Convert progress debug")
				}
				updater.UpdateProgress("converting", pct, processed, imageSize)
			}
		}
	}

	output := outBuf.Bytes()
	if executionErr != nil {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"error":       executionErr,
			"exit_code":   exitCode,
			"output":      string(output),
		}).Error("Volume population command failed")
		return fmt.Errorf("failed to populate LVM volume: %w, output: %s", executionErr, string(output))
	}

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
	}).Info("Volume population command completed successfully")

	// Calculate and save rate.
	duration := time.Since(startTime)
	rateBPS := float64(imageSize) / duration.Seconds()
	if store != nil {
		saveErr := store.SaveStageRate(ctx, storage.StageRate{
			Stage:          "convert",
			RateBPS:        rateBPS,
			BytesProcessed: imageSize,
			DurationMS:     duration.Milliseconds(),
			JobID:          jobID,
			CreatedAt:      time.Now(),
		})
		if saveErr != nil {
			logrus.WithError(saveErr).Warn("failed to save stage rate")
		}
	}

	if updater != nil {
		updater.UpdateProgress("converting", 100, imageSize, imageSize)
	}

	return nil
}

// DeleteVolume deletes an LVM volume
func (m *Manager) DeleteVolume(ctx context.Context, volumeName string) error {
	// Start span for LVM volume deletion
	_, span := otel.Tracer("libvirt-volume-provisioner").Start(ctx, "DeleteVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.String("lvm.vg_name", m.vgName)))
	defer span.End()

	if !m.volumeExists(ctx, volumeName) {
		span.SetStatus(codes.Error, "volume does not exist")
		return fmt.Errorf("volume %s does not exist", volumeName)
	}

	cmd := lvmCmd(ctx, "lvremove", "-f", fmt.Sprintf("%s/%s", m.vgName, volumeName))
	output, err := cmd.CombinedOutput()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"error":       err,
			"output":      string(output),
		}).Error("Failed to delete LVM volume")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to delete LVM volume %s: %w", volumeName, err)
	}

	span.SetStatus(codes.Ok, "volume deleted successfully")
	return nil
}

// GetVolumeInfo returns information about an LVM volume
func (m *Manager) GetVolumeInfo(ctx context.Context, volumeName string) (*VolumeInfo, error) {
	if !m.volumeExists(ctx, volumeName) {
		return nil, fmt.Errorf("volume %s does not exist", volumeName)
	}

	fullPath := fmt.Sprintf("%s/%s", m.vgName, volumeName)
	cmd := lvmCmd(ctx, "lvs", "--units", "b", "--noheadings",
		"-o", "lv_name,lv_size,lv_attr", fullPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get volume info: %w, output: %s", err, string(output))
	}

	// Parse output
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 3 {
		return nil, fmt.Errorf("unexpected lvs output format")
	}

	sizeStr := strings.TrimSuffix(fields[1], "B")
	sizeBytes, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse volume size: %w", err)
	}

	return &VolumeInfo{
		Name:       fields[0],
		SizeBytes:  sizeBytes,
		Attributes: fields[2],
	}, nil
}

// ListVolumes returns a list of all LVM volumes in the volume group
func (m *Manager) ListVolumes() ([]string, error) {
	cmd := lvmCmd(context.Background(), "lvs", "--noheadings", "-o", "lv_name", m.vgName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w, output: %s", err, string(output))
	}

	var volumes []string
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if volume := strings.TrimSpace(line); volume != "" {
			volumes = append(volumes, volume)
		}
	}

	return volumes, nil
}

// volumeExists checks if an LVM volume exists
func (m *Manager) volumeExists(ctx context.Context, volumeName string) bool {
	cmd := lvmCmd(ctx, "lvs", fmt.Sprintf("%s/%s", m.vgName, volumeName))
	return cmd.Run() == nil
}

// validateExistingVolume checks if an existing volume is compatible for reuse
func (m *Manager) validateExistingVolume(ctx context.Context, volumeName string, requiredSizeGB int) error {
	info, err := m.GetVolumeInfo(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("failed to get volume info: %w", err)
	}

	// Check size (allow some tolerance for filesystem overhead)
	requiredSizeBytes := int64(requiredSizeGB) * 1000 * 1000 * 1000
	actualSizeBytes := info.SizeBytes

	// Allow 5% variance for filesystem/formatting differences
	sizeTolerance := requiredSizeBytes / 20 // 5%
	minSize := requiredSizeBytes - sizeTolerance

	if actualSizeBytes < minSize {
		return fmt.Errorf("existing volume size %d bytes too small, need at least %d bytes",
			actualSizeBytes, minSize)
	}
	// Note: Larger volumes are acceptable - no need to reject them

	// Check if volume is active/available (field 4 of lvs attribute string)
	if len(info.Attributes) < 5 {
		return fmt.Errorf("unexpected lvs attribute format %q", info.Attributes)
	}
	if info.Attributes[4] != 'a' && info.Attributes[4] != '-' {
		return fmt.Errorf("volume state '%c' not suitable for reuse", info.Attributes[4])
	}

	return nil
}

// VolumeInfo represents information about an LVM volume
type VolumeInfo struct {
	Name       string
	SizeBytes  int64
	Attributes string
}
