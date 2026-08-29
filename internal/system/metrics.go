package system

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type HostMetrics struct {
	CPUUsagePercent float64
	MemoryUsedMB    uint64
	MemoryTotalMB   uint64
	MemoryPercent   float64
	DiskUsedGB      float64
	DiskTotalGB     float64
	DiskPercent     float64
	UptimeFormatted string
}

type SSLMetrics struct {
	Issuer        string
	Subject       string
	NotBefore     time.Time
	NotAfter      time.Time
	DaysRemaining int
	IsValid       bool
	SelfSigned    bool
}

func GetHostMetrics(dataDir string) HostMetrics {
	var m HostMetrics

	// Uptime
	if uptimeData, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(uptimeData))
		if len(fields) > 0 {
			if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				d := time.Duration(secs) * time.Second
				days := int(d.Hours()) / 24
				hours := int(d.Hours()) % 24
				mins := int(d.Minutes()) % 60
				if days > 0 {
					m.UptimeFormatted = fmt.Sprintf("%dd %dh %dm", days, hours, mins)
				} else {
					m.UptimeFormatted = fmt.Sprintf("%dh %dm", hours, mins)
				}
			}
		}
	}
	if m.UptimeFormatted == "" {
		m.UptimeFormatted = "unknown"
	}

	// Memory
	if memData, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, freeKB, availableKB, buffersKB, cachedKB uint64
		for _, line := range strings.Split(string(memData), "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			valFields := strings.Fields(parts[1])
			if len(valFields) == 0 {
				continue
			}
			val, _ := strconv.ParseUint(valFields[0], 10, 64)
			switch key {
			case "MemTotal":
				totalKB = val
			case "MemFree":
				freeKB = val
			case "MemAvailable":
				availableKB = val
			case "Buffers":
				buffersKB = val
			case "Cached":
				cachedKB = val
			}
		}
		if totalKB > 0 {
			var usedKB uint64
			if availableKB > 0 {
				usedKB = totalKB - availableKB
			} else {
				usedKB = totalKB - freeKB - buffersKB - cachedKB
			}
			m.MemoryTotalMB = totalKB / 1024
			m.MemoryUsedMB = usedKB / 1024
			m.MemoryPercent = float64(usedKB) / float64(totalKB) * 100
		}
	}

	// Disk
	targetPath := "/"
	if dataDir != "" {
		targetPath = dataDir
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(targetPath, &stat); err == nil {
		totalBytes := stat.Blocks * uint64(stat.Bsize)
		freeBytes := stat.Bavail * uint64(stat.Bsize)
		usedBytes := totalBytes - freeBytes
		if totalBytes > 0 {
			m.DiskTotalGB = float64(totalBytes) / (1024 * 1024 * 1024)
			m.DiskUsedGB = float64(usedBytes) / (1024 * 1024 * 1024)
			m.DiskPercent = float64(usedBytes) / float64(totalBytes) * 100
		}
	}

	// CPU
	m.CPUUsagePercent = getCPUPercent()

	return m
}

func getCPUPercent() float64 {
	readStat := func() (idle, total uint64, err error) {
		data, err := os.ReadFile("/proc/stat")
		if err != nil {
			return 0, 0, err
		}
		lines := strings.Split(string(data), "\n")
		if len(lines) == 0 {
			return 0, 0, fmt.Errorf("empty /proc/stat")
		}
		fields := strings.Fields(lines[0])
		if len(fields) < 5 || fields[0] != "cpu" {
			return 0, 0, fmt.Errorf("invalid cpu format")
		}
		var sum uint64
		for i := 1; i < len(fields); i++ {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			sum += v
		}
		idleVal, _ := strconv.ParseUint(fields[4], 10, 64)
		return idleVal, sum, nil
	}

	idle1, total1, err1 := readStat()
	if err1 != nil {
		return 0.0
	}
	time.Sleep(100 * time.Millisecond)
	idle2, total2, err2 := readStat()
	if err2 != nil || total2 <= total1 {
		return 0.0
	}

	idleDelta := float64(idle2 - idle1)
	totalDelta := float64(total2 - total1)
	return (1.0 - (idleDelta / totalDelta)) * 100.0
}

func GetSSLMetrics(installDir, domain string) (*SSLMetrics, error) {
	certPath := filepath.Join(installDir, "certs/certificates", domain+".crt")
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("certificate file not found: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM certificate")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}

	now := time.Now()
	days := int(time.Until(cert.NotAfter).Hours() / 24)
	isSelfSigned := cert.Subject.String() == cert.Issuer.String()

	issuerName := cert.Issuer.CommonName
	if issuerName == "" && len(cert.Issuer.Organization) > 0 {
		issuerName = cert.Issuer.Organization[0]
	}
	if issuerName == "" {
		issuerName = cert.Issuer.String()
	}

	return &SSLMetrics{
		Issuer:        issuerName,
		Subject:       cert.Subject.CommonName,
		NotBefore:     cert.NotBefore,
		NotAfter:      cert.NotAfter,
		DaysRemaining: days,
		IsValid:       now.After(cert.NotBefore) && now.Before(cert.NotAfter),
		SelfSigned:    isSelfSigned,
	}, nil
}
