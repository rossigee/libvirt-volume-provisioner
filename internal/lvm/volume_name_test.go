package lvm

import "testing"

func TestValidateVolumeName(t *testing.T) {
	valid := []string{
		"data",
		"data-01",
		"data_01",
		"DATA",
		"vol.with.dots",
		"vg0+data",
		"a",
		"0",
	}
	for _, name := range valid {
		if err := validateVolumeName(name); err != nil {
			t.Errorf("validateVolumeName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []struct {
		name string
		why  string
	}{
		{"", "empty"},
		{"../etc/shadow", "parent reference"},
		{"..", "bare parent"},
		{".", "bare current"},
		{"data/../other", "embedded parent"},
		{"/dev/sda", "absolute path"},
		{"data/child", "path separator"},
		{`data\child`, "backslash separator"},
		{"data;rm -rf /", "shell metacharacter"},
		{"data$(id)", "command substitution"},
		{"data name", "space"},
		{"dätá", "non-ascii"},
		{"data\n", "newline"},
	}
	for _, tc := range invalid {
		if err := validateVolumeName(tc.name); err == nil {
			t.Errorf("validateVolumeName(%q) = nil, want error (%s)", tc.name, tc.why)
		}
	}
}

// A name that escapes the volume group's device directory is the whole point of
// the guard, so pin that case explicitly against the path it would have built.
func TestValidateVolumeNameRejectsDevicePathEscape(t *testing.T) {
	const vg = "data"
	for _, name := range []string{"../sda", "../../etc/shadow", "x/../../y"} {
		if err := validateVolumeName(name); err == nil {
			t.Errorf("validateVolumeName(%q) = nil, want error; it would build /dev/%s/%s", name, vg, name)
		}
	}
}

// LVM accepts these characters in a volume name, so the guard must not be
// stricter than LVM or it would reject legitimate names.
func TestValidateVolumeNameMatchesLVMCharset(t *testing.T) {
	// LVM's documented restriction for names is [a-zA-Z0-9+_.-]
	for _, r := range "abcXYZ019+_.-" {
		name := "vol" + string(r)
		if err := validateVolumeName(name); err != nil {
			t.Errorf("validateVolumeName(%q) = %v, want nil; %q is valid in an LVM name", name, err, r)
		}
	}
}
