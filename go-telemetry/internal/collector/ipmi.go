// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

package collector

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
)

// IPMIConfig configures the IPMI collector.
type IPMIConfig struct {
	// Host is the BMC address (used for the RMCP presence ping and ipmitool).
	Host string
	// Port is the RMCP UDP port (623 default).
	Port int
	// Username / Password passed to ipmitool for sensor reads.
	Username string
	Password string
	// IPMITool is the path to the ipmitool binary (default "ipmitool").
	IPMITool string
}

// IPMI implements Collector for IPMI-enabled BMCs.
//
// The RMCP/ASF presence ping is implemented in-process over UDP. Detailed
// sensor reads delegate to the ipmitool binary, which is the pragmatic,
// battle-tested transport most operators already have on the edge host. A
// future in-process IPMI 2.0 RAKP session can replace the ipmitool leg
// without changing the Collector surface.
type IPMI struct {
	cfg IPMIConfig
}

// NewIPMI builds an IPMI collector.
func NewIPMI(cfg IPMIConfig) *IPMI {
	if cfg.Port == 0 {
		cfg.Port = 623
	}
	if cfg.IPMITool == "" {
		cfg.IPMITool = "ipmitool"
	}
	return &IPMI{cfg: cfg}
}

// Name reports the collector name.
func (i *IPMI) Name() string { return "ipmi" }

// Poll first verifies BMC presence via RMCP, then reads sensors via ipmitool.
func (i *IPMI) Poll(ctx context.Context) ([]point.DataPoint, error) {
	if err := i.ping(ctx); err != nil {
		return nil, err
	}
	return i.sensors(ctx)
}

// ping sends an RMCP/ASF presence ping and waits for the RMCP ack.
func (i *IPMI) ping(ctx context.Context) error {
	addr := net.JoinHostPort(i.cfg.Host, strconv.Itoa(i.cfg.Port))
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return fmt.Errorf("ipmi: dial %s: %w", addr, err)
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	} else {
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	}
	// RMCP header (0x06 version, ASF class 0x06) + ASF ping (IANA 0x00011BE,
	// type 0x80).
	ping := []byte{
		0x06, 0x00, 0x00, 0x06,
		0xBE, 0x11, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	if _, err := conn.Write(ping); err != nil {
		return fmt.Errorf("ipmi: ping write: %w", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("ipmi: ping read: %w", err)
	}
	// RMCP ack has class 0x06 and ASF message type 0x40 (pong).
	if n < 8 || buf[3] != 0x06 || buf[7] != 0x40 {
		return fmt.Errorf("ipmi: unexpected ping response %x", buf[:n])
	}
	return nil
}

// sensors runs `ipmitool sdr` and parses the output into points.
func (i *IPMI) sensors(ctx context.Context) ([]point.DataPoint, error) {
	tool := i.cfg.IPMITool
	args := []string{"-H", i.cfg.Host, "-U", i.cfg.Username, "-P", i.cfg.Password, "-I", "lanplus", "sdr"}
	out, err := runCmd(ctx, tool, args...)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: ipmitool unavailable for %s", ErrNotImplemented, i.cfg.Host)
		}
		return nil, fmt.Errorf("ipmi: sdr: %w", err)
	}
	return parseSDR(i.cfg.Host, out), nil
}

// parseSDR converts ipmitool `sdr` text into DataPoints.
//
// Example line: "Temp1 | 25.000 | degrees C | ok"
func parseSDR(host, text string) []point.DataPoint {
	var out []point.DataPoint
	now := time.Now()
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		unitStr := strings.TrimSpace(parts[2])
		var value float64
		if _, err := fmt.Sscanf(valStr, "%f", &value); err != nil {
			continue
		}
		metric, ok := unitToMetric(unitStr)
		if !ok {
			continue
		}
		out = append(out, point.New("bmc:"+host, "sensor:"+name, metric, value, now))
	}
	return out
}

func unitToMetric(unit string) (point.Metric, bool) {
	switch strings.ToLower(unit) {
	case "degrees c", "c", "celsius":
		return point.MetricTemperature, true
	case "volts", "v":
		return point.MetricVoltage, true
	case "amps", "amperes", "a":
		return point.MetricAmperage, true
	case "watts", "w":
		return point.MetricPower, true
	case "rpm":
		return point.MetricFanSpeed, true
	default:
		return "", false
	}
}

func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}

func isNotFound(err error) bool {
	var ee *exec.Error
	if errors.As(err, &ee) {
		return ee.Err == exec.ErrNotFound
	}
	return false
}
