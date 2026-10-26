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

		logs := filepath.Join(m.opts.DataDir, "logs")
		switch logChoice {
		case "singbox":
			printLogFileTail(filepath.Join(logs, "sing-box.log"), "/var/log/sing-box.log", "Sing-box")
		case "nginx_access":
			printLogFileTail(filepath.Join(logs, "nginx", "access.log"), "/var/log/nginx/access.log", "Nginx Access")
		case "nginx_error":
			printLogFileTail(filepath.Join(logs, "nginx", "error.log"), "/var/log/nginx/error.log", "Nginx Error")
		case "tg":
			printLogFileTail(filepath.Join(logs, "tproxy-server.log"), "/var/log/tproxy-server.log", "Telegram Web Proxy")
		case "live":
			m.streamLiveLogs(ctx)
		}
	}
}

// printLogFileTail prints the last 50 lines of path, or of fallback when path does not exist.
func printLogFileTail(path, fallback, title string) {
	if _, err := os.Stat(path); err != nil {
		path = fallback
	}
	fmt.Printf("\n=== %s (Last 50 lines) ===\n", title)
	out, err := exec.Command("tail", "-n", "50", path).CombinedOutput()
	if err != nil || len(out) == 0 {
		fmt.Printf("Log file %s is empty or not found.\n\n", path)
		return
	}
	fmt.Println(string(out))
	fmt.Println("==============================")
}

func (m *Menu) streamLiveLogs(ctx context.Context) {
	fmt.Println("\nStreaming live logs... (Press Ctrl+C to return to menu)")

	cmd := exec.CommandContext(ctx, "tail", "-f", "-n", "20", filepath.Join(m.opts.LogDir, "access.log"))
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
