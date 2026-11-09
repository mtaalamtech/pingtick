package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

type SpeedTestResult struct {
	Timestamp     time.Time `json:"timestamp"`
	DownloadSpeed float64   `json:"download_speed"` // in Mbps
	UploadSpeed   float64   `json:"upload_speed"`   // in Mbps
	Latency       int64     `json:"latency"`        // in ms
	IsRunning     bool      `json:"is_running"`
	ISP           string    `json:"isp"`
	IPAddr        string    `json:"ip_address"`
}

var (
	currentSpeedTest = &SpeedTestResult{}
)

func RunSpeedTest() {
	if currentSpeedTest.IsRunning {
		return
	}
	currentSpeedTest.IsRunning = true
	log.Println("Starting scheduled internet speed test...")

	// 0. Resolve ISP via ip-api
	respIP, errIP := http.Get("http://ip-api.com/json/")
	if errIP == nil {
		var ipData struct {
			ISP   string `json:"isp"`
			Query string `json:"query"`
		}
		if err := json.NewDecoder(respIP.Body).Decode(&ipData); err == nil {
			currentSpeedTest.ISP = ipData.ISP
			currentSpeedTest.IPAddr = ipData.Query
		}
		respIP.Body.Close()
	}

	// 1. Measure Latency to Cloudflare
	startPing := time.Now()
	resp, err := http.Get("https://speed.cloudflare.com/")
	var latency int64
	if err == nil {
		latency = time.Since(startPing).Milliseconds()
		resp.Body.Close()
	} else {
		latency = 999
	}
	currentSpeedTest.Latency = latency

	// 2. Measure Download Speed (5MB)
	// We download 5,000,000 bytes from Cloudflare speed test API
	startDown := time.Now()
	respDown, err := http.Get("https://speed.cloudflare.com/__down?bytes=5000000")
	if err != nil {
		log.Printf("Download speed test failed: %v", err)
		currentSpeedTest.DownloadSpeed = 0
	} else {
		// Read entire body to memory
		buf := make([]byte, 32*1024)
		var totalBytes int64 = 0
		for {
			n, readErr := respDown.Body.Read(buf)
			totalBytes += int64(n)
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				log.Printf("Error reading download stream: %v", readErr)
				break
			}
		}
		respDown.Body.Close()

		duration := time.Since(startDown).Seconds()
		if duration > 0 {
			// bytes to bits -> / 1024 / 1024 to megabits
			megabits := float64(totalBytes*8) / 1000000.0
			currentSpeedTest.DownloadSpeed = megabits / duration
		} else {
			currentSpeedTest.DownloadSpeed = 0
		}
	}

	// 3. Measure Upload Speed (1MB)
	// POST 1,000,000 bytes to Cloudflare speed test upload API
	uploadData := make([]byte, 1000000)
	startUp := time.Now()
	respUp, err := http.Post("https://speed.cloudflare.com/__up", "application/octet-stream", bytes.NewReader(uploadData))
	if err != nil {
		log.Printf("Upload speed test failed: %v", err)
		currentSpeedTest.UploadSpeed = 0
	} else {
		io.Copy(io.Discard, respUp.Body)
		respUp.Body.Close()

		duration := time.Since(startUp).Seconds()
		if duration > 0 {
			megabits := float64(len(uploadData)*8) / 1000000.0
			currentSpeedTest.UploadSpeed = megabits / duration
		} else {
			currentSpeedTest.UploadSpeed = 0
		}
	}

	currentSpeedTest.Timestamp = time.Now()
	currentSpeedTest.IsRunning = false
	log.Printf("Speedtest Finished. Down: %.2f Mbps, Up: %.2f Mbps, Ping: %dms",
		currentSpeedTest.DownloadSpeed, currentSpeedTest.UploadSpeed, currentSpeedTest.Latency)
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
