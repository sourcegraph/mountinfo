//go:build !(linux || darwin || windows)

package mountinfo

import (
	"fmt"
	"runtime"
)

func discoverDeviceName(logger discoveryLogger, filePath string) (string, error) {
	return "", fmt.Errorf("not implemented on %s", runtime.GOOS)
}
