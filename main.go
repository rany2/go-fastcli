package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/term"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Create HTTP transports to share pool of connections while disabling compression
var tr = &http.Transport{DisableCompression: true}
var client = &http.Client{Transport: tr}

// BytesCounter implements io.Reader interface, for counting bytes being written in HTTP requests
type BytesCounter struct {
	uploadchan chan uint
}

func NewCounter() *BytesCounter {
	return &BytesCounter{}
}

// Read implements io.Reader for Upload tests
func (c *BytesCounter) Read(p []byte) (int, error) {
	for i, b := range bytes.Repeat([]byte("a"), len(p)) {
		p[i] = b
	}
	c.uploadchan <- uint(len(p))
	return len(p), nil
}

// Format range of the fast.com test URL
func FormatFastURL(url string, rangeEnd int) string {
	return strings.Replace(url, "/speedtest?", "/speedtest/range/0-"+strconv.Itoa(rangeEnd)+"?", -1)
}

// Ping URL function
func PingURL(url string, done chan bool, result chan float64) {
	defer AnnounceDeath(done)
	req, _ := http.NewRequest("GET", url, nil)
	var t1, t2 time.Time
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) {
			t1 = time.Now()
		},
		ConnectDone: func(net, addr string, err error) {
			t2 = time.Now()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	_, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		log.Fatal(err)
	}
	result <- float64(time.Duration(t2.Sub(t1))) / 1000000
}

// To announce the death of the goroutine
func AnnounceDeath(done chan bool) {
	done <- true
}

func DownloadFile(url string, done chan bool, timeout int64, speed chan uint) {
	// Send done to channel on death of function
	defer AnnounceDeath(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)

	// Make sure operation is cancelled on exit
	defer cancel()

	// Make new request to download URL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	// Associate the cancellable context we just created to the request
	req = req.WithContext(ctx)

	// Execute the request
	for { // run until limit
		resp, err := client.Do(req)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return
			} else {
				log.Fatal(err)
			}
		}
		defer resp.Body.Close()

		// Read body response in chunks to measure speed
		reader := bufio.NewReader(resp.Body)
		part := make([]byte, 32768) // reasonable buffer
		for {
			count, err := reader.Read(part)
			if err != nil {
				break
			} else {
				speed <- uint(len(string(part[:count])))
			}
		}
	}
}

func UploadFile(url string, done chan bool, timeout int64, speed chan uint) {
	// Send done to channel on death of function
	defer AnnounceDeath(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)

	// Make sure operation is cancelled on exit
	defer cancel()

	// Create counter to measure upload speed
	counter := NewCounter()
	counter.uploadchan = speed

	// Make new request to download URL
	req, err := http.NewRequest("POST", url, counter)
	req.ContentLength = int64(26214400)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Content-type", "application/octet-stream")
	if err != nil {
		log.Fatal(err)
	}
	// Associate the cancellable context we just created to the request
	req = req.WithContext(ctx)

	// Execute the request
	for { // run until limit
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()

		// Read body response in chunks
		reader := bufio.NewReader(resp.Body)
		part := make([]byte, 32768)
		for {
			_, err := reader.Read(part)
			if err != nil {
				break
			}
		}
	}
}

func main() {
	latencyBool := flag.Bool("latency", false, "run latency test")
	downloadBool := flag.Bool("download", false, "run download test")
	uploadBool := flag.Bool("upload", false, "run upload test")
	parallelPerURL := flag.Int("test_per_url", 2, "run test x times per url")
	maxTimeInTest := flag.Int64("test_time", 30, "total time for each test")
	urlsToTest := flag.Int("url_to_test", 5, "number of urls to get from api")
	flag.Parse()

	testsToRun := []string{}
	if !*downloadBool && !*uploadBool && !*latencyBool {
		*downloadBool = true
		*uploadBool = true
		*latencyBool = true
	}
	if *latencyBool {
		testsToRun = append(testsToRun, "latency")
	}
	if *downloadBool {
		testsToRun = append(testsToRun, "download")
	}
	if *uploadBool {
		testsToRun = append(testsToRun, "upload")
	}

	resp, err := http.Get("https://api.fast.com/netflix/speedtest/v2?https=true&token=YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm&urlCount=" + strconv.Itoa(*urlsToTest))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatal("API returned %d. Expected 200.", resp.StatusCode)
	}

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &m); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Testing from %s, AS%s, %s, %s.\n",
		m["client"].(map[string]interface{})["ip"],
		m["client"].(map[string]interface{})["asn"],
		m["client"].(map[string]interface{})["location"].(map[string]interface{})["city"],
		m["client"].(map[string]interface{})["location"].(map[string]interface{})["country"])
	/*fmt.Printf("Testing to %d servers:\n", len(m["targets"].([]interface{})))
	for d, i := range m["targets"].([]interface{}) {
		fmt.Printf("  %d: %s, %s.\n", d+1,
			i.(map[string]interface{})["location"].(map[string]interface{})["city"],
			i.(map[string]interface{})["location"].(map[string]interface{})["country"])
	}*/
	fmt.Println()

	currentSpeed := make(chan uint, 1)
	doneChan := make(chan bool, 1)
	pingChan := make(chan float64, 1)
	for _, test := range testsToRun {
		totalTimes := 0 // to detect when all download goroutines are done
		for _, i := range m["targets"].([]interface{}) {
			if test == "latency" {
				var testUrl = FormatFastURL(i.(map[string]interface{})["url"].(string), 0)
				go PingURL(testUrl, doneChan, pingChan)
				totalTimes++
			} else {
				var testUrl = FormatFastURL(i.(map[string]interface{})["url"].(string), 26214400)
				for j := 0; j < *parallelPerURL; j++ { // 8 parallel for now
					if test == "download" {
						go DownloadFile(testUrl, doneChan, *maxTimeInTest, currentSpeed)
					} else if test == "upload" {
						go UploadFile(testUrl, doneChan, *maxTimeInTest, currentSpeed)
					}
					totalTimes++
				}
			}
		}

		var startTime = time.Now().Unix()
		var totalDl = uint(0)
		var totalPing []float64
		var speedMbps = float64(0)
	outer:
		for {
			select {
			case c := <-currentSpeed:
				totalDl += c
				speedMbps = float64(totalDl) / float64(time.Now().Unix()-startTime) / 125000
				if term.IsTerminal(syscall.Stdout) {
					if test == "download" {
						fmt.Printf("\r\033[KDownload: %0.3f Mbps", speedMbps)
					} else {
						fmt.Printf("\r\033[KUpload: %0.3f Mbps", speedMbps)
					}
				}
			case c := <-pingChan:
				totalPing = append(totalPing, c)
			case <-doneChan:
				totalTimes--
				if totalTimes == 0 {
					if test == "latency" {
						var m = float64(0)
						for i, e := range totalPing {
							if i == 0 || e < m {
								m = e
							}
						}
						fmt.Printf("Ping: %0.3f ms", m)
					} else if !term.IsTerminal(syscall.Stdout) {
						if test == "download" {
							fmt.Printf("Download: %0.3f Mbps", speedMbps)
						} else if test == "upload" {
							fmt.Printf("Upload: %0.3f Mbps", speedMbps)
						}
					}
					break outer
				}
			default:
				time.Sleep(1 * time.Millisecond)
			}
		}
		fmt.Println()
	}
}
