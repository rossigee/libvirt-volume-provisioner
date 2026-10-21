// Package libvirt provides functionality for managing libvirt storage pools and volumes,
// including image caching and allocation for the libvirt-volume-provisioner.
package libvirt

import (
	"encoding/xml"
	"fmt"
	"path/filepath"

	"github.com/libvirt/libvirt-go"
)

// domainDiskXML is the part of a libvirt domain descriptor the volume guard
// needs: the disk sources a guest is attached to.
type domainDiskXML struct {
	Devices struct {
		Disks []struct {
			Source struct {
				Dev  string `xml:"dev,attr"`
				File string `xml:"file,attr"`
			} `xml:"source"`
		} `xml:"disk"`
	} `xml:"devices"`
}

// FindRunningDomainUsingVolume reports the name of a running domain that has
// the named volume attached, if there is one.
//
// The provisioner writes an image straight into the block device of the target
// volume. If a guest is running on that device, the write corrupts the guest's
// filesystem: the image overwrites the mounted filesystem while the kernel has
// it open, and the guest comes back with a read-only root filesystem
// ("errors=remount-ro") or a torn filesystem.
//
// The volume name is matched against the last path element of every disk
// source, which is the volume name for both /dev/<vg>/<volume> and
// /dev/mapper/<vg>-<volume> device paths.
//
// Only active domains are considered: a domain that is defined but shut off
// holds no open file handle on the device and is safe to populate.
func (pm *PoolManager) FindRunningDomainUsingVolume(volumeName string) (string, bool, error) {
	if pm.conn == nil {
		return "", false, fmt.Errorf("libvirt connection is not initialised")
	}

	domains, err := pm.conn.ListAllDomains(libvirt.CONNECT_LIST_DOMAINS_RUNNING)
	if err != nil {
		return "", false, fmt.Errorf("listing domains: %w", err)
	}

	for i := range domains {
		dom := &domains[i]
		name, err := dom.GetName()
		if err != nil {
			continue
		}
		xmlDesc, err := dom.GetXMLDesc(0)
		if err != nil {
			continue
		}
		var parsed domainDiskXML
		if err := xml.Unmarshal([]byte(xmlDesc), &parsed); err != nil {
			continue
		}
		if domainUsesVolume(parsed, volumeName) {
			return name, true, nil
		}
	}

	return "", false, nil
}

// domainUsesVolume reports whether any disk of the domain is backed by the
// named volume.
func domainUsesVolume(parsed domainDiskXML, volumeName string) bool {
	for _, disk := range parsed.Devices.Disks {
		for _, source := range []string{disk.Source.Dev, disk.Source.File} {
			if source == "" {
				continue
			}
			if filepath.Base(source) == volumeName {
				return true
			}
		}
	}
	return false
}
