// Package mountinfo provides a Prometheus collector that advertises
// the names of block storage devices backing the requested file paths.
package mountinfo

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	sglog "github.com/sourcegraph/log"
)

// CollectorOpts modifies the behavior of the metric created
// by NewCollector.
type CollectorOpts struct {
	// If non-empty, Namespace prefixes the "mount_point_info" metric by the provided string and
	// an underscore ("_").
	Namespace string
}

// NewCollector returns a Prometheus collector that collects a single metric, "mount_point_info",
// that contains the names of the block storage devices backing each of the requested mounts.
//
// Mounts is a set of name -> file path mappings (example: {"indexDir": "/home/.zoekt"}).
//
// The metric "mount_point_info" has a constant value of 1 and two labels:
//   - mount_name: caller-provided name for the given mount (example: "indexDir")
//   - device: name of the block device that backs the given mount file path (example: "sdb")
//
// This metric currently works only on Linux-based operating systems that have access to the sysfs pseudo-filesystem.
// On all other operating systems, this metric will not emit any values.
func NewCollector(logger sglog.Logger, opts CollectorOpts, mounts map[string]string) prometheus.Collector {
	logger = logger.Scoped("mountPointInfo").Scoped("deviceNameDiscovery")
	return newCollector(func(name, filePath string) discoveryLogger {
		return sglogAdapter{logger.With(
			sglog.String("mountName", name),
			sglog.String("mountFilePath", filePath),
		)}
	}, opts, mounts)
}

// NewSlogCollector is like NewCollector, but logs to a log/slog logger.
func NewSlogCollector(logger *slog.Logger, opts CollectorOpts, mounts map[string]string) prometheus.Collector {
	return newCollector(func(name, filePath string) discoveryLogger {
		return logger.With("mountName", name, "mountFilePath", filePath)
	}, opts, mounts)
}

// discoveryLogger is the logging the collector does while discovering
// devices. *slog.Logger implements it, and sglogAdapter implements it for
// github.com/sourcegraph/log.
type discoveryLogger interface {
	Debug(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

func newCollector(newLogger func(name, filePath string) discoveryLogger, opts CollectorOpts, mounts map[string]string) prometheus.Collector {
	metric := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: opts.Namespace,
		Name:      "mount_point_info",
		Help:      "An info metric with a constant '1' value that contains mount_name, device mappings",
	}, []string{"mount_name", "device"})

	for name, filePath := range mounts {
		// for each <mountName>:<mountFilePath> pairing,
		// discover the name of the block device that stores <mountFilePath>.
		logger := newLogger(name, filePath)

		device, err := discoverDeviceName(logger, filePath)
		if err != nil {
			// Check if this is a virtual filesystem - don't warn for those
			var virtualFsErr *virtualFilesystemError
			if errors.As(err, &virtualFsErr) {
				logger.Debug("skipping metric registration for virtual filesystem",
					"deviceNumber", virtualFsErr.DeviceNumber,
				)
				continue
			}

			// For block devices that failed, log as warning
			logger.Warn("skipping metric registration",
				"reason", "failed to discover device name",
				"error", err,
			)

			continue
		}

		logger.Debug("discovered device name",
			"deviceName", device,
		)

		metric.WithLabelValues(name, device).Set(1)
	}

	return metric
}

// sglogAdapter implements discoveryLogger for github.com/sourcegraph/log. It
// supports the arguments the collector passes: string values, and an error
// under the "error" key.
type sglogAdapter struct {
	l sglog.Logger
}

func (a sglogAdapter) Debug(msg string, args ...any) { a.l.Debug(msg, sglogFields(args)...) }
func (a sglogAdapter) Warn(msg string, args ...any)  { a.l.Warn(msg, sglogFields(args)...) }
func (a sglogAdapter) Error(msg string, args ...any) { a.l.Error(msg, sglogFields(args)...) }

func sglogFields(args []any) []sglog.Field {
	var fields []sglog.Field
	for i := 0; i+1 < len(args); i += 2 {
		key := fmt.Sprint(args[i])
		if err, ok := args[i+1].(error); ok && key == "error" {
			fields = append(fields, sglog.Error(err))
		} else {
			fields = append(fields, sglog.String(key, fmt.Sprint(args[i+1])))
		}
	}
	return fields
}

// virtualFilesystemError is returned when a filesystem is virtual/pseudo
// and doesn't have a block device
type virtualFilesystemError struct {
	DeviceNumber string
}

func (e *virtualFilesystemError) Error() string {
	return fmt.Sprintf("virtual filesystem (device %s)", e.DeviceNumber)
}
