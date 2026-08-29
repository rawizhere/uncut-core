package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
)

func (m *Menu) showLogsMenu(ctx context.Context) {
	for {
		var logChoice string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select Service Log:").
					Options(
						huh.NewOption("1. Sing-box Logs", "singbox"),
						huh.NewOption("2. Nginx Access Log", "nginx_access"),
						huh.NewOption("3. Nginx Error Log", "nginx_error"),
						huh.NewOption("4. Telegram Web Proxy Logs", "tg"),
						huh.NewOption("5. Live Daemon Stream", "live"),
						huh.NewOption("0. Back", "back"),
					).
					Value(&logChoice),
			),
		)

		if err := form.Run(); err != nil || logChoice == "back" || logChoice == "" {
			return
		}

		switch logChoice {
		case "singbox":
			sbPath := "/opt/uncut/data/logs/sing-box.log"
			if _, err := os.Stat(sbPath); err != nil {
				sbPath = "/var/log/sing-box.log"
			}
			m.printLogFileTail(sbPath, 50, "Sing-box")
		case "nginx_access":
			accessPath := filepath.Join("/opt/uncut/data/logs/nginx/access.log")
			if _, err := os.Stat(accessPath); err != nil {
				accessPath = "/var/log/nginx/access.log"
			}
			m.printLogFileTail(accessPath, 50, "Nginx Access")
		case "nginx_error":
			errorPath := filepath.Join("/opt/uncut/data/logs/nginx/error.log")
			if _, err := os.Stat(errorPath); err != nil {
				errorPath = "/var/log/nginx/error.log"
			}
			m.printLogFileTail(errorPath, 50, "Nginx Error")
		case "tg":
			tgPath := "/opt/uncut/data/logs/tproxy-server.log"
			if _, err := os.Stat(tgPath); err != nil {
				tgPath = "/var/log/tproxy-server.log"
			}
			m.printLogFileTail(tgPath, 50, "Telegram Web Proxy")
		case "live":
			m.streamLiveLogs(ctx)
		}
	}
}

func (m *Menu) printLogFileTail(path string, lines int, title string) {
	fmt.Printf("\n=== %s (Last %d lines) ===\n", title, lines)
	if _, err := os.Stat(path); err != nil {
		// Fallback to journalctl or supervisor logs
		cmd := exec.Command("tail", "-n", fmt.Sprintf("%d", lines), path)
		out, err := cmd.CombinedOutput()
		if err != nil || len(out) == 0 {
			fmt.Printf("Log file %s is empty or not found.\n\n", path)
			return
		}
		fmt.Println(string(out))
		return
	}

	cmd := exec.Command("tail", "-n", fmt.Sprintf("%d", lines), path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Error reading log: %v\n", err)
	} else {
		fmt.Println(string(out))
	}
	fmt.Println("==============================")
}

func (m *Menu) streamLiveLogs(ctx context.Context) {
	fmt.Println("\nStreaming live logs... (Press Ctrl+C to return to menu)")

	cmd := exec.CommandContext(ctx, "tail", "-f", "-n", "20", "/var/log/nginx/access.log")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("Failed to open stream: %v\n\n", err)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("Failed to start log stream: %v\n\n", err)
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			fmt.Println(line)
		}
	}

	_ = cmd.Wait()
	fmt.Println("\nStopped live stream.")
}
