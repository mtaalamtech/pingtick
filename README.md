# PingTick 🚀

A premium, lightweight, and portable network monitoring tool designed for system and network administrators. Written in **Go** and featuring an embedded **Glassmorphic Web Dashboard**, PingTick provides real-time SLA metrics, TCP port inspections, SSL expiry alerts, and automatic speed tests—all packaged into a single, dependency-free binary.

---

## Key Features

1. **Concurrent Inspection Service**:
   - **ICMP Ping**: Real-time ping latency track.
   - **TCP Port Handshake**: Inspect service accessibility on custom ports (e.g., databases, web servers).
   - **SSL Cert Expiry**: Monitors SSL/TLS expiration dates for websites and warns at 30, 14, 7 and 1 days before expiry.
   - **IP Scanner**: Finds live devices on a range of up to 4096 addresses and adds them to monitoring in one click.
   
2. **Interactive Glassmorphic Interface**:
   - Real-time page values updated dynamically (polls every 2s).
   - Sidebar selector to switch views seamlessly between **Dashboard**, **Manage Targets**, and **Alerts Config**.
   - Beautiful **Chart.js** historical graphs to track target latencies.
   - Modals for adding nodes and inspecting historical timelines.

3. **Speed Test Gateway**:
   - On-demand and automated download/upload/ping measurement powered by Cloudflare edge servers.

4. **Robust Notification & Alerting**:
   - Anti-flap threshold limits to suppress false warnings.
   - Instant notifications via **Telegram Channels** and **SMTP E-Mails**.

5. **Self-Contained Portability**:
   - Built-in **SQLite database** (`modernc.org/sqlite` - no CGO required).
   - Embedded frontend web templates (`go:embed`).
   - Dynamic port selection to prevent Tomcat, Node, or PostgreSQL port collisions.

---

## Project Structure

```
pingtick/
├── main.go            # HTTP server, REST endpoints, browser launcher
├── monitor.go         # Core check scheduler (concurrent routines)
├── db.go              # SQLite migrations, target CRUD, settings storage
├── ssl.go             # SSL TLS socket expiry inspector
├── speedtest.go       # ISP Speedcheck (down/up metrics via Cloudflare)
├── alerts.go          # SMTP mailer & Telegram hook
└── frontend/          # Embedded static client files
    ├── index.html     
    ├── style.css      
    └── script.js      
```

---

## Getting Started

### Prerequisites

To compile PingTick from source, you only need Go installed:
- Go (v1.20+)

### Building PingTick

To compile PingTick into a single portable binary:

```bash
# Compile
go build -ldflags "-s -w -H=windowsgui" -o pingtick.exe

# Run
./pingtick.exe
```

When started, PingTick will:
1. Initialize/connect to `pingtick.db`.
2. Spin up the background monitoring tasks.
3. Automatically bind to the first available listener port on your localhost.
4. Open the native monitor window. Closing it keeps monitoring in the system tray, and **Web Dashboard** opens settings, reports and the IP scanner in a browser.

---

## Alert Configuration Guide

### 1. Telegram Notifications
1. Direct message `@BotFather` on Telegram and create a new bot. Copy the **Token API ID**.
2. Add your bot to a channel or chat group, check the ID (e.g., `-100...`), and save details under **Alerts Config**.

### 2. Email SMTP Options
1. Enter your SMTP Host address (`smtp.gmail.com`) and port (`587`).
2. Input credentials and warning recipient e-mail address. Click **Save Settings** to confirm.
