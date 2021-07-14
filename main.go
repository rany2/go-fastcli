package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"golang.org/x/term"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Create HTTP transports to share pool of connections while disabling compression
var tr = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	DisableCompression:    true,
}
var client = &http.Client{
	Transport: tr,
}

// BytesCounter implements io.Reader interface, for counting bytes being written in HTTP requests
type BytesCounter struct {
	speedChannel chan uint
	readIndex    int64
	maxIndex     int64
}

func NewCounter() *BytesCounter {
	return &BytesCounter{}
}

// Read implements io.Reader for Upload tests
func (c *BytesCounter) Read(p []byte) (n int, err error) {
	if c.readIndex >= c.maxIndex {
		err = io.EOF
		return
	}

	for i, _ := range p {
		p[i] = 'a'
	}

	n = len(p)
	c.readIndex += int64(n)
	c.speedChannel <- uint(n)
	return
}

// Write implements io.Writer for Download tests
func (c *BytesCounter) Write(p []byte) (n int, err error) {
	n = len(p)
	c.speedChannel <- uint(len(p))
	return
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

func DownloadFile(url string, done chan bool, timeout float64, speed chan uint) {
	// Send done to channel on death of function
	defer AnnounceDeath(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)

	// Make sure operation is cancelled on exit
	defer cancel()

	// Create counter to measure download speed
	counter := NewCounter()
	counter.speedChannel = speed

	// Make new request to download URL
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	// Associate the cancellable context we just created to the request
	req = req.WithContext(ctx)

	// Execute the request until we reach the timeout
	for {
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		io.Copy(counter, resp.Body)
	}
}

func UploadFile(url string, done chan bool, timeout float64, speed chan uint) {
	// Send done to channel on death of function
	defer AnnounceDeath(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)

	// Make sure operation is cancelled on exit
	defer cancel()

	// Create counter to measure upload speed
	counter := NewCounter()
	counter.speedChannel = speed
	counter.maxIndex = int64(26214400)

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

	// Execute the request until we reach the timeout
	for {
		// Reset the index back to 0
		counter.readIndex = int64(0)
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
	}
}

func main() {
	latencyBool := flag.Bool("latency", false, "run latency test")
	downloadBool := flag.Bool("download", false, "run download test")
	uploadBool := flag.Bool("upload", false, "run upload test")
	parallelPerURL := flag.Int("test-per-url", 2, "run test x times per url")
	maxTimeInTest := flag.Float64("test-time", 30, "total time for each test")
	urlsToTest := flag.Int("url-to-test", 5, "number of urls to request from api")
	pingTimes := flag.Int("ping-times", 8, "for latency test ping url x times")
	ipv4Bool := flag.Bool("ipv4", false, "use ipv4")
	ipv6Bool := flag.Bool("ipv6", false, "use ipv6")
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

	var dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	defaultDialer := http.DefaultTransport.(*http.Transport).Clone()
	if *ipv4Bool && *ipv6Bool {
		log.Fatal("-ipv4 and -ipv6 are mutually exclusive")
	} else if *ipv4Bool {
		dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, "tcp4", address)
		}
	} else if *ipv6Bool {
		dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, "tcp6", address)
		}
	} else {
		dialContext = defaultDialer.DialContext
	}
	tr.DialContext = dialContext
	api_tr := http.DefaultTransport.(*http.Transport).Clone()
	api_tr.DialContext = dialContext
	http.DefaultClient.Transport = api_tr

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
	fmt.Printf("Testing from %s, AS%s, %s, %s\n",
		m["client"].(map[string]interface{})["ip"],
		m["client"].(map[string]interface{})["asn"],
		m["client"].(map[string]interface{})["location"].(map[string]interface{})["city"],
		m["client"].(map[string]interface{})["location"].(map[string]interface{})["country"])
	fmt.Printf("Testing to %d servers:\n", len(m["targets"].([]interface{})))
	for d, i := range m["targets"].([]interface{}) {
		u, err := url.Parse(i.(map[string]interface{})["url"].(string))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %d) %s, %s, %s\n", d+1,
			u.Host,
			i.(map[string]interface{})["location"].(map[string]interface{})["city"],
			i.(map[string]interface{})["location"].(map[string]interface{})["country"])
	}
	fmt.Println()

	currentSpeed := make(chan uint, 1)
	doneChan := make(chan bool, 1)
	pingChan := make(chan float64, 1)
	for _, test := range testsToRun {
		totalTimes := 0 // to detect when all goroutines are done
		for _, i := range m["targets"].([]interface{}) {
			if test == "latency" {
				var testUrl = FormatFastURL(i.(map[string]interface{})["url"].(string), 0)
				for j := 0; j < *pingTimes; j++ {
					go PingURL(testUrl, doneChan, pingChan)
					totalTimes++
				}
			} else {
				var testUrl = FormatFastURL(i.(map[string]interface{})["url"].(string), 26214400)
				for j := 0; j < *parallelPerURL; j++ {
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
						fmt.Printf("\r\033[KDownload   %0.3f Mbps", speedMbps)
					} else {
						fmt.Printf("\r\033[KUpload     %0.3f Mbps", speedMbps)
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
						fmt.Printf("Ping       %0.3f ms", m)
					} else if !term.IsTerminal(syscall.Stdout) {
						if test == "download" {
							fmt.Printf("Download   %0.3f Mbps", speedMbps)
						} else if test == "upload" {
							fmt.Printf("Upload     %0.3f Mbps", speedMbps)
						}
					}
					break outer
				}
			}
		}
		fmt.Println()
	}
}
