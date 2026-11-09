package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type SpeedTestResult struct {
	Timestamp     time.Time `json:"timestamp"`
	DownloadSpeed float64   `json:"download_speed"` // in Mbps
	UploadSpeed   float64   `json:"upload_speed"`   // in Mbps
	Latency       int64     `json:"latency"`        // in ms
	IsRunning     bool      `json:"is_running"`
	ISP           string    `json:"isp,omitempty"`
	IPAddr        string    `json:"ip_address,omitempty"`
}

var (
	currentSpeedTest SpeedTestResult
	speedTestMu      sync.Mutex
)

func speedTestSnapshot() SpeedTestResult {
	speedTestMu.Lock()
	defer speedTestMu.Unlock()
	return currentSpeedTest
}

// publicIPVisible is off unless explicitly enabled, so the public IP and ISP
// stay off screen during remote sessions.
func publicIPVisible() bool {
	return getSetting("show_public_ip") == "true"
}

// speedTestForDisplay is the only view of the result that leaves the server.
func speedTestForDisplay() SpeedTestResult {
	result := speedTestSnapshot()
	if !publicIPVisible() {
		result.ISP = ""
		result.IPAddr = ""
	}
	return result
}

func RunSpeedTest() {
	speedTestMu.Lock()
	if currentSpeedTest.IsRunning {
		speedTestMu.Unlock()
		return
	}
	currentSpeedTest.IsRunning = true
	speedTestMu.Unlock()

	log.Println("Starting scheduled internet speed test...")

	// A timeout guarantees a hung connection can never block later tests.
	client := &http.Client{Timeout: 60 * time.Second}
	defer client.CloseIdleConnections()

	var isp, ipAddr string
	if publicIPVisible() {
		isp, ipAddr = lookupISP(client)
	}
	latency := measureLatency(client)
	download := measureDownload(client)
	upload := measureUpload(client)

	speedTestMu.Lock()
	currentSpeedTest = SpeedTestResult{
		Timestamp:     time.Now(),
		DownloadSpeed: download,
		UploadSpeed:   upload,
		Latency:       latency,
		ISP:           isp,
		IPAddr:        ipAddr,
	}
	speedTestMu.Unlock()

	log.Printf("Speedtest Finished. Down: %.2f Mbps, Up: %.2f Mbps, Ping: %dms", download, upload, latency)
}

func lookupISP(client *http.Client) (isp, ipAddr string) {
	resp, err := client.Get("http://ip-api.com/json/")
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var data struct {
		ISP   string `json:"isp"`
		Query string `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", ""
	}
	return data.ISP, data.Query
}

func measureLatency(client *http.Client) int64 {
	start := time.Now()
	resp, err := client.Get("https://speed.cloudflare.com/")
	if err != nil {
		return 999
	}
	resp.Body.Close()
	return time.Since(start).Milliseconds()
}

// measureDownload streams 5 MB from Cloudflare and returns the speed in Mbps.
func measureDownload(client *http.Client) float64 {
	start := time.Now()
	resp, err := client.Get("https://speed.cloudflare.com/__down?bytes=5000000")
	if err != nil {
		log.Printf("Download speed test failed: %v", err)
		return 0
	}
	defer resp.Body.Close()

	totalBytes, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		log.Printf("Error reading download stream: %v", err)
	}
	return megabitsPerSecond(totalBytes, time.Since(start))
}

// measureUpload posts 1 MB to Cloudflare and returns the speed in Mbps.
func measureUpload(client *http.Client) float64 {
	data := make([]byte, 1000000)
	start := time.Now()
	resp, err := client.Post("https://speed.cloudflare.com/__up", "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		log.Printf("Upload speed test failed: %v", err)
		return 0
	}
	defer resp.Body.Close()

	io.Copy(io.Discard, resp.Body)
	return megabitsPerSecond(int64(len(data)), time.Since(start))
}

func megabitsPerSecond(bytes int64, elapsed time.Duration) float64 {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return 0
	}
	return float64(bytes*8) / 1000000.0 / seconds
}

func StartAutoSpeedTest() {
	// Wait 10 seconds before starting the first automatic benchmark
	time.AfterFunc(10*time.Second, func() {
		RunSpeedTest() // initial
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			RunSpeedTest()
		}
	})
}
