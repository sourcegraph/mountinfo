//go:build linux

package mountinfo

import (
	"log"
	"os"
	"testing"

	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"

	"github.com/google/go-cmp/cmp"
	"github.com/sourcegraph/log/logtest"
)

func Test_DeviceName_SmokeTest(t *testing.T) {
	// A simple smoke test to verify that we can find the storage device
	// for the current working directory.
	// NOTE: CWD must be on a block device. Cannot be, for example, a Docker bind mount. CWD _can_ be a Docker volume.
	logger := logtest.Scoped(t)

	filePath, err := os.Getwd()
	if err != nil {
		log.Fatalf("getting current working directory: %s", err)
	}

	device, err := discoverDeviceName(logger, filePath)
	if err != nil {
		t.Fatalf("Unable to find device name for path %q: %s", filePath, err)
	}

	t.Logf("discovered device name %q for path %q", device, filePath)
}

func Test_DeviceName_Snapshots(t *testing.T) {
	// This test uses sysfs snapshots from real linux machines to ensure
	// that the device discovery logic returns the expected device name.

	for _, test := range []struct {
		name string

		sysfsTarballFile string

		deviceMajor uint32
		deviceMinor uint32

		expectedDeviceName string
	}{
		{
			name: "should find the name of the block device that backs a partition (vda1 -> vda)",

			// ( lsblk output from the snapshotted machine)
			// ~ # lsblk
			// NAME   MAJ:MIN RM  SIZE RO TYPE MOUNTPOINTS
			// nbd0    43:0    0    0B  0 disk
			// nbd1    43:32   0    0B  0 disk
			// nbd2    43:64   0    0B  0 disk
			// nbd3    43:96   0    0B  0 disk
			// nbd4    43:128  0    0B  0 disk
			// nbd5    43:160  0    0B  0 disk
			// nbd6    43:192  0    0B  0 disk
			// nbd7    43:224  0    0B  0 disk
			// vda    254:0    0 59.6G  0 disk
			// └─vda1 254:1    0 59.6G  0 part /etc/hosts  # test targets this partition
			//                                 /etc/hostname
			//                                 /etc/resolv.conf
			//                                 /data/index
			// nbd8    43:256  0    0B  0 disk
			// nbd9    43:288  0    0B  0 disk
			// nbd10   43:320  0    0B  0 disk
			// nbd11   43:352  0    0B  0 disk
			// nbd12   43:384  0    0B  0 disk
			// nbd13   43:416  0    0B  0 disk
			// nbd14   43:448  0    0B  0 disk
			// nbd15   43:480  0    0B  0 disk

			sysfsTarballFile: "sysfs.vda1.tar.gz",

			deviceMajor: 254, // points to vda1 partition
			deviceMinor: 1,

			expectedDeviceName: "vda",
		},
		{
			name: "should find the device name for a lvm volume backed by a single disk",

			// ( lsblk output from the snapshotted machine)
			// ~ # lsblk
			// NAME           MAJ:MIN RM  SIZE RO TYPE MOUNTPOINTS
			// sda              8:0    0  7.3T  0 disk
			// └─sda1           8:1    0 1024G  0 part /var/lib/plex
			// nvme0n1        259:0    0  1.8T  0 disk
			// ├─nvme0n1p1    259:1    0  529M  0 part
			// ├─nvme0n1p2    259:2    0   99M  0 part
			// ├─nvme0n1p3    259:3    0   16M  0 part
			// ├─nvme0n1p4    259:4    0  293G  0 part
			// ├─nvme0n1p5    259:5    0  512M  0 part /boot
			// └─nvme0n1p6    259:6    0  1.5T  0 part
			//   └─pool-nixos 254:0    0  600G  0 lvm  /nix/store
			//                                         / # test targets this device

			sysfsTarballFile: "sysfs.lvm.dm-0.tar.gz",

			deviceMajor: 254, // points to dm-0 device
			deviceMinor: 0,

			// TODO@ggilmore: technically, dm-0 is a lvm volume backed by a partition stored on the nvme device.
			// For consistency with the other test case, we should be returning nvme0n1 (the parent disk device) as the
			// device name. I'll revisit this later, as I need to figure out how to programmatically determine
			// the nvme01n1 <-> dm-0 translation.
			expectedDeviceName: "dm-0",
		},
	} {
		test := test

		t.Run(t.Name(), func(t *testing.T) {
			t.Parallel()

			// provide a custom sysfs location so that we can point the test
			// at our sysfs snapshot
			mockSysFSDir := filepath.Join(t.TempDir(), "sys")

			// unpack sysfs tarball
			tarball := filepath.Join("testdata", test.sysfsTarballFile)
			decompressSysFSTarball(t, tarball, mockSysFSDir)

			// now that our fake sysfs filesystem is in place, clean the path representation
			// and resolve any symbolic links because further processing does the same thing
			// which can make path comparisons fail when they shouldn't
			mockSysFSDir, err := filepath.EvalSymlinks(filepath.Clean(mockSysFSDir))
			if err != nil {
				t.Fatalf("unable to set up temporary sysfs location: %s", err)
			}

			logger := logtest.Scoped(t)

			fakeFilePath := "doesn't matter" // the file path itself doesn't matter since we hard-code the device number

			// redefine functions with alternate behavior
			findSysfsMountpoint = func() (mountpoint string, err error) {
				return mockSysFSDir, nil
			}
			getDeviceNumber = func(filePath string) (deviceNumber string, err error) {
				return fmt.Sprintf("%d:%d", test.deviceMajor, test.deviceMinor), nil
			}

			// execute the test with our injected mocks
			actualDeviceName, err := discoverDeviceName(logger, fakeFilePath)

			if err != nil {
				t.Fatalf("discovering device name for file path %q: %s", fakeFilePath, err)
			}

			// verify that the discovered device name is the one that we expect

			if diff := cmp.Diff(test.expectedDeviceName, actualDeviceName); diff != "" {
				t.Fatalf("recieved unexpected device name (-want +got):\n%s", diff)
			}
		})
	}
}

func decompressSysFSTarball(t *testing.T, tarball, outputFolder string) {
	t.Helper()

	file, err := os.Open(tarball)
	if err != nil {
		t.Fatalf("opening tarball %q: %s", tarball, err)
	}

	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("initialzing gzip reader: %s", err)
	}

	reader := tar.NewReader(gz)

	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			t.Fatalf("intializing tar reader: %s", err)
		}

		outputFile := filepath.Join(outputFolder, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			err := os.MkdirAll(outputFile, os.FileMode(header.Mode))
			if err != nil {
				t.Fatalf("creating directory %q: %s", outputFile, err)
			}

		case tar.TypeSymlink:
			err := os.Symlink(header.Linkname, outputFile)
			if err != nil {
				t.Fatalf("creating symlink (%q -> %q): %s", outputFile, header.Linkname, err)
			}

		case tar.TypeReg:
			f, err := os.OpenFile(outputFile, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				t.Fatalf("creating file %q: %s", outputFile, err)
			}

			_, err = io.Copy(f, reader)
			if err != nil {
				t.Fatalf("writing file %q: %s", outputFile, err)
			}

			f.Close()

		default:
			t.Fatalf("encounted unknown file header type (%d) for file %q", header.Typeflag, header.Name)
		}
	}
}
