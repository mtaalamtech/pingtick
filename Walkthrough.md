# PingTick - Application Walkthrough

Welcome to **PingTick**! This comprehensive guide walks you through using your brand new network monitoring tool, exploring its interfaces, capabilities, and best practices.

---

## 1. Navigating the Interface

When you run `pingtick.exe`, the background monitoring engine boots up instantly and your default web browser is launched to `http://localhost:<dynamic_port>/`. Your UI uses a sleek, dark-themed glassmorphism layout split into three primary tabs:

- **Dashboard**: The heart of your operations. Here, you get a real-time (2-second polling) overview of all monitored endpoints, their current SLA health, average latencies, and total packet metrics.
- **Manage Targets**: Your control center for defining which IP addresses, routing paths, or websites to track across the global internet or your local LAN.
- **Alerts Config**: A dedicated form to input SMTP Email gateways or Telegram API tokens so PingTick can ping you automatically if a service crashes.

---

## 2. Managing Targets

PingTick supports four distinct types of network probes. To get started, navigate to **Manage Targets**, click **+ Add New Target**, and select your protocol:

1. **Raw ICMP Ping Check**: 
   - Useful for: Tracking exact round-trip physical delays to routers, VMs, or ISPs. 
2. **TCP Port Handshake Check**:
   - Useful for: Seeing if a background service (like a database on port 5432) is actively accepting connections.
3. **SSL Cert Expiry Monitor**:
   - Useful for: Preventing website downtimes caused by forgotten SSL renewals.
4. **DNS Resolution Benchmark**:
   - Useful for: Tracking upstream DNS resolver latency issues, finding out how long a hostname takes to return its pointer IP.

*Bonus Trick*: You can also input a custom **Tag Group** (e.g. `Database`, `US-East`) to dynamically label and categorize your tracked endpoints everywhere in your app.

---

## 3. Reviewing System Insights

Head back to the **Dashboard** Tab once you have targets configured.
- See real-world connection durability instantly through the **Overall Uptime** stat block.
- Observe the **Reply IP Address** column to see how hostnames resolve. 
- Look at the **Min/Max/Avg** splits to spot bandwidth stability.

For a deeper dive, click the **Insights** button on any target row. A large overlay modal will present:
- An interactive **Chart.js** generated line graph showing ping fluctuations over time.
- A **Scrolling History Log Table** tracking every single packet's Sent Timestamp, Reply IP, exact Latency (ms), and TTL (Time-to-Live) trace count.
- **Export CSV Button**: Found at the top right of the Insights modal, clicking this will instantly bundle up to the last 5000 check results for a target and download them to your machine for offline Excel/Grafana analysis!

---

## 4. On-Demand Internet Speed Testing

At the top of the **Dashboard** sits the `Internet Speed Test Gateway`.
- The Gateway automatically queries your **Public ISP Provider and Edge IP**.
- Click **Run Speed Test**. You will see the button state change to _Testing..._.
- The Go backend will spin up an isolated worker that streams packets to/from Cloudflare's globally distributed Edge servers. 
- Once completed, you will receive real-world **Download (Mbps)**, **Upload (Mbps)**, and bare-metal **ISP Latency (ms)** metrics.

---

## 5. Setting Up Crash Alerts (Anti-Flap Engine)

To save you from network fatigue, PingTick includes an *Anti-Flap* algorithm. If a server drops a single packet, we don't spam your email. Instead, configure the rules in **Alerts Config**:

- Set a **Fail Count Threshold** (e.g., 3 checks before alerting).
- Enable **Telegram** by passing a Bot Token and Channel ID. You will receive emergency messages right to your phone when a target goes fully offline, and a subsequent "Recovery" message when it comes back online.
- Enable **Email Alerts** by plugging in an SMTP application password (such as a Gmail App Password).

All these configurations are safely recorded in a blazing-fast local `pingtick.db` file alongside your target lists.

You are now fully equipped to keep your critical infrastructure monitored seamlessly out-of-the-box!
