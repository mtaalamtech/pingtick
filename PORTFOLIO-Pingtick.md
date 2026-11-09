# PingTick - Enterprise Endpoint & Network Monitoring Suite

**Role:** Full-Stack Systems Developer  
**Status:** Completed  
**Type:** Standalone Network Daemon / Web Application  

---

## 📌 Project Overview
PingTick is a zero-configuration, ultra-lightweight network monitoring daemon designed to rival heavy enterprise software (like PRTG or Zabbix) without the massive overhead. Compiled as a single executable, PingTick natively binds its own internal SQLite database, background worker engine, and embedded web server. It provides IT Administrators with a sleek, glassmorphism-inspired web dashboard for real-time latency analytics, endpoint health, and network bandwidth tracking.

## 🚀 Key Features

* **Zero-Dependency Architecture:** Entirely self-contained in a single `.exe`. The frontend HTML, CSS, JavaScript, and compiled PNG/ICO graphics are directly injected into the Go binary memory using native `//go:embed` directives.
* **Multi-Protocol Polling Engine:**
  * **ICMP Ping:** Real-time TTL and Latency charting.
  * **TCP Port Polling:** Application service health checks (e.g., MySQL 3306, HTTP 80).
  * **SSL/TLS Certificate Tracking:** Proactive domain expiry monitoring.
  * **SNMP (Simple Network Management Protocol):** Aggregates automated router/switch throughput bandwidth.
  * **WMI (Windows Management Instrumentation):** Natively queries remote Windows servers via WQL for active network interfaces and byte calculation.
* **Automated Alerting & Reporting:** 
  * Granular daily status reporting pushed automatically via Telegram Bot API or authenticated SMTP Email.
  * 1-Click Analytical Reports outputting cleanly formatted `.CSV` and `.PDF` documents (via embedded `gofpdf` vector rendering).
* **Automated Speed Testing:** Built-in speed test engine capturing download/upload speeds and ISP drops over timed intervals.
* **Native System Integration:** Binds natively to the Windows System Tray with custom, dynamically generated binary `.ico` payloads, allowing the main engine to run silently as a background service without an open browser tab.

## 🛠️ Technology Stack & Architecture

* **Backend / Core Engine:** Go (Golang)
* **Web Server:** Native `net/http` (Localhost listener mechanism with zero-port contention logic)
* **Database / Persistence:** SQLite (`modernc.org/sqlite` - CGO-free, fully cross-platform)
* **Frontend UI/UX:** Vanilla JavaScript, HTML5, CSS3 
* **Visualization:** Chart.js 
* **Low-Level Libraries:** `go-winres` (Exe metadata injection), `systray` (OS daemon integration)

## 🧠 Technical Challenges & Solutions

* **File Explorer & Compiler Caching:** Overcame aggressive Windows OS graphical caching and Golang compiler build caches when generating custom `.ico` binaries byte-by-byte mathematically (256x256 checkmarks mapped to `Bits-Per-Pixel` structural headers).
* **Multi-Threaded Concurrency:** Designed a robust multi-threaded Go worker pipeline using `go routines` and `sync.Mutex` locks to poll dozens of endpoints simultaneously (Ping, WMI, SNMP) without crashing the internal SQLite database or hitting IO bottlenecks.
* **Browser Sandbox Escaping:** Implemented Exit-Intent listeners and dynamic parent-window closing logic in JavaScript to allow the local web dashboard to act/close like a native desktop UI without violating modern browser security constraints.

## 💼 Business Value & Target Audience
PingTick is engineered for **Small-to-Medium Businesses (SMBs), Managed Service Providers (MSPs), and IT System Administrators** who need immediate network visibility. By stripping away complicated database configurations, active-directory setups, and heavy installation processes, PingTick allows an administrator to deploy a fully functional, enterprise-grade monitoring dashboard to any server in less than three seconds.
