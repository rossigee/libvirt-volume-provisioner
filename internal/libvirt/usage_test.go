package libvirt

import (
	"encoding/xml"
	"testing"
)

// The disk source of a domain that boots from an LVM volume looks like
// <source dev='/dev/data/<volume>'/>, and the cloud-init ISO is attached as
// a file. The guard must match the volume and ignore everything else.
func TestDomainUsesVolume(t *testing.T) {
	const descriptor = `<domain type='kvm'>
  <name>itx-staging-workernode-1.bankrut.lan</name>
  <devices>
    <disk type='block' device='disk'>
      <driver name='qemu' type='raw'/>
      <source dev='/dev/data/itx-staging-workernode-1.bankrut.lan-root' index='3'/>
      <target dev='sda' bus='sata'/>
    </disk>
    <disk type='block' device='disk'>
      <source dev='/dev/data/itx-staging-workernode-1.bankrut.lan-lvm' index='2'/>
      <target dev='sdb' bus='sata'/>
    </disk>
    <disk type='file' device='cdrom'>
      <source file='/var/lib/libvirt/cloud-init/cidata-itx-staging-workernode-1.bankrut.lan.iso' index='1'/>
      <target dev='sdd' bus='sata'/>
    </disk>
  </devices>
</domain>`

	var parsed domainDiskXML
	if err := xml.Unmarshal([]byte(descriptor), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cases := []struct {
		volume string
		want   bool
	}{
		{"itx-staging-workernode-1.bankrut.lan-root", true},
		{"itx-staging-workernode-1.bankrut.lan-lvm", true},
		{"cidata-itx-staging-workernode-1.bankrut.lan.iso", true},
		{"itx-staging-workernode-2.bankrut.lan-root", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := domainUsesVolume(parsed, tc.volume); got != tc.want {
			t.Errorf("domainUsesVolume(%q) = %v, want %v", tc.volume, got, tc.want)
		}
	}
}

// A domain with no disks at all must not match anything.
func TestDomainUsesVolumeNoDisks(t *testing.T) {
	const descriptor = `<domain type='kvm'><name>empty</name><devices/></domain>`

	var parsed domainDiskXML
	if err := xml.Unmarshal([]byte(descriptor), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if domainUsesVolume(parsed, "anything") {
		t.Error("a domain without disks must not match a volume")
	}
}
