package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strconv"
	"sync"
	"time"
)

var (
	// Track consecutive failures per target
	consecutiveFailures = make(map[string]int)
	// Track whether target is currently in "alerted" (offline) state
	alertedState = make(map[string]bool)
	alertMutex   = &sync.Mutex{}
)

func init() {
	go startDailyReportScheduler()
}

func startDailyReportScheduler() {
	for {
		now := time.Now()
		nextMinute := now.Truncate(time.Minute).Add(time.Minute)
		time.Sleep(time.Until(nextMinute))

		if getSetting("daily_report_enabled") != "true" {
			continue
		}

		targetTime := getSetting("daily_report_time")
		if targetTime == "" {
			targetTime = "08:00"
		}

		if time.Now().Format("15:04") == targetTime {
			sendDailyReport()
		}
	}
}

func sendDailyReport() {
	telegramEnabled := getSetting("telegram_enabled") == "true"
	emailEnabled := getSetting("email_enabled") == "true"
	if !telegramEnabled && !emailEnabled {
		return
	}

	reports, err := GetGlobalReport("yesterday", "", "")
	if err != nil {
		log.Printf("Failed to gen daily report: %v", err)
		return
	}

	up := 0
	down := 0
	totalUptime := 0.0
	for _, rp := range reports {
		if rp.Current == "online" {
			up++
		} else {
			down++
		}
		totalUptime += rp.UptimePct
	}

	avgUptime := 0.0
	if len(reports) > 0 {
		avgUptime = totalUptime / float64(len(reports))
	}

	msg := fmt.Sprintf("📊 *PingTick Daily Summary (Yesterday)*\n\n"+
		"Total Targets: %d\nCurrently Online: %d\nCurrently Offline: %d\nAverage Uptime: %.2f%%\n\n",
		len(reports), up, down, avgUptime)

	log.Println("Sending Daily Automated Report...")
	sendNotifications(msg)
}

func getSetting(key string) string {
	var val string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return ""
	}
	return val
}

func saveSetting(key, value string) error {
	_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}

func checkAlerting(t *Target) {
	alertMutex.Lock()
	defer alertMutex.Unlock()

	thresholdStr := getSetting("alert_threshold")
	threshold := 3
	if thresholdStr != "" {
		if val, err := strconv.Atoi(thresholdStr); err == nil {
			threshold = val
		}
	}

	if t.Status == "offline" {
		consecutiveFailures[t.ID]++
		failures := consecutiveFailures[t.ID]

		log.Printf("[DEBUG] Target %s is offline. Fail Count: %d / %d", t.Name, failures, threshold)

		// Trigger alert if threshold is reached and we haven't already alerted
		if failures >= threshold && !alertedState[t.ID] {
			alertedState[t.ID] = true
			msg := fmt.Sprintf("🚨 Alert: Target %s (%s) is OFFLINE! Latency: %dms. Time: %s",
				t.Name, t.Host, t.Latency, time.Now().Format("2006-01-02 15:04:05"))
			log.Println(msg)
			go sendNotifications(msg)
		}
	} else if t.Status == "online" {
		failures := consecutiveFailures[t.ID]
		consecutiveFailures[t.ID] = 0

		// Trigger recovery if we were previously in an alerted state
		if alertedState[t.ID] {
			alertedState[t.ID] = false
			msg := fmt.Sprintf("✅ Recovery: Target %s (%s) is back ONLINE. Latency: %dms. (Failed checks: %d). Time: %s",
				t.Name, t.Host, t.Latency, failures, time.Now().Format("2006-01-02 15:04:05"))
			log.Println(msg)
			go sendNotifications(msg)
		}
	}
}

// sendNotifications returns true if at least one enabled channel accepted the message.
func sendNotifications(message string) bool {
	delivered := false

	// 1. Send Telegram
	telegramEnabled := getSetting("telegram_enabled") == "true"
	if telegramEnabled {
		token := getSetting("telegram_bot_token")
		chatID := getSetting("telegram_chat_id")
		if token != "" && chatID != "" {
			err := sendTelegramMessage(token, chatID, message)
			if err != nil {
				log.Printf("Failed to send Telegram alert: %v", err)
			} else {
				delivered = true
			}
		}
	}

	// 2. Send Email
	emailEnabled := getSetting("email_enabled") == "true"
	if emailEnabled {
		host := getSetting("smtp_host")
		port := getSetting("smtp_port")
		user := getSetting("smtp_user")
		pass := getSetting("smtp_pass")
		from := getSetting("smtp_from")
		protocol := getSetting("smtp_protocol")
		to := getSetting("alert_email_to")

		if host != "" && port != "" && user != "" && to != "" {
			if from == "" {
				from = user
			}
			err := sendEmailAlert(host, port, user, pass, from, to, protocol, message)
			if err != nil {
				log.Printf("Failed to send Email alert: %v", err)
			} else {
				delivered = true
			}
		}
	}
	return delivered
}

// sendTestNotification returns an error that is safe to show in the dashboard.
func sendTestNotification(triggerType, message string) error {
	if triggerType == "telegram" {
		token := getSetting("telegram_bot_token")
		chatID := getSetting("telegram_chat_id")
		if token == "" || chatID == "" {
			return errors.New("save the Telegram bot token and chat ID first")
		}
		if err := sendTelegramMessage(token, chatID, message); err != nil {
			// The error can contain the request URL, which includes the bot token.
			log.Printf("Failed to send test Telegram alert: %v", err)
			return errors.New("Telegram could not be reached or rejected the bot details")
		}
		return nil
	} else if triggerType == "email" {
		host := getSetting("smtp_host")
		port := getSetting("smtp_port")
		user := getSetting("smtp_user")
		pass := getSetting("smtp_pass")
		from := getSetting("smtp_from")
		protocol := getSetting("smtp_protocol")
		to := getSetting("alert_email_to")

		if host != "" && port != "" && user != "" && to != "" {
			if from == "" {
				from = user
			}
			err := sendEmailAlert(host, port, user, pass, from, to, protocol, message)
			if err != nil {
				log.Printf("Failed to send test Email alert: %v", err)
			}
			return err
		}
		return errors.New("save the SMTP host, port, user and recipient first")
	}
	return errors.New("unknown alert type")
}

func sendTelegramMessage(token, chatID, text string) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]string{
		"chat_id": chatID,
		"text":    text,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(apiURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API returned status: %d", resp.StatusCode)
	}

	return nil
}

type loginAuth struct {
	username, password string
}

func LoginAuth(username, password string) smtp.Auth {
	return &loginAuth{username, password}
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(a.username), nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		switch string(fromServer) {
		case "Username:":
			return []byte(a.username), nil
		case "Password:":
			return []byte(a.password), nil
		default:
			return nil, errors.New("Unknown from server")
		}
	}
	return nil, nil
}
