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

// Fast Specific Values
const FastMaxPayload = 26214400
const FastApiToken = "YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm"

// Create HTTP transports to share pool of connections while disabling compression
var tr = &http.Transport{
	Proxy: nil, //http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	DisableCompression:    true,
	MaxIdleConns:          0,
	MaxIdleConnsPerHost:   0,
	MaxConnsPerHost:       0,
	WriteBufferSize:       0,
	ReadBufferSize:        0,
}
var client = &http.Client{
	Transport: tr,
}

func calculateSpeed(totalDl float64, startTime time.Time) float64 {
	return float64(totalDl) / time.Now().Sub(startTime).Seconds() / 125000
}

// To announce the death of the goroutine
func AnnounceDeath(done chan bool) {
	done <- true
}

func FormatFastURL(url string, rangeEnd int) string {
	return strings.Replace(url, "/speedtest?", "/speedtest/range/0-"+strconv.Itoa(rangeEnd)+"?", -1)
}

func PingURL(url string, done chan bool, result chan float64) {
	defer AnnounceDeath(done)

	req, _ := http.NewRequest("GET", url, nil)
	var t1, t2 time.Time
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) {
			t1 = time.Now()
		},
		ConnectDone: func(_, _ string, _ error) {
			t2 = time.Now()
		},
	}

	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	_, err := tr.RoundTrip(req)
	if err != nil {
		log.Fatal(err)
	}

	result <- float64(time.Duration(t2.Sub(t1))) / 1000000
}

// BytesCounter implements io.Reader interface, for counting bytes being written in HTTP requests
type BytesCounter struct {
	SpeedChannel chan uint
	ReadIndex    int64
	MaxIndex     int64
}

func NewCounter() *BytesCounter {
	return &BytesCounter{}
}

// Read implements io.Reader for Upload tests
func (c *BytesCounter) Read(p []byte) (n int, err error) {
	if c.ReadIndex >= c.MaxIndex {
		err = io.EOF
		return
	}

	for i := range p {
		p[i] = 'a'
	}
	n = len(p)

	c.ReadIndex += int64(n)
	c.SpeedChannel <- uint(n)
	return
}

// Write implements io.Writer for Download tests
func (c *BytesCounter) Write(p []byte) (n int, err error) {
	n = len(p)
	c.SpeedChannel <- uint(len(p))
	return
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
	counter.SpeedChannel = speed

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

func UploadFile(url string, done chan bool, timeout float64, speed chan uint, uploadinit *int) {
	// Send done to channel on death of function
	defer AnnounceDeath(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)

	// Make sure operation is cancelled on exit
	defer cancel()

	// Create counter to measure upload speed
	counter := NewCounter()
	counter.SpeedChannel = speed
	counter.MaxIndex = int64(FastMaxPayload)

	// Make new request to download URL
	req, err := http.NewRequest("POST", url, counter)
	req.ContentLength = int64(FastMaxPayload)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Content-type", "application/octet-stream")
	if err != nil {
		log.Fatal(err)
	}
	// Associate the cancellable context we just created to the request
	req = req.WithContext(ctx)

	// Execute the request until we reach the timeout
	for {
		// So that initial byte doesn't count
		*uploadinit--

		// Reset the index back to 0
		counter.ReadIndex = int64(0)
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

	ipv4Bool := flag.Bool("ipv4", false, "force use ipv4")
	ipv6Bool := flag.Bool("ipv6", false, "force use ipv6")

	pingTimes := flag.Int("ping-times", 1, "for latency test ping url n times")
	downTimes := flag.Int("download-test-per-url", 8, "run download test n times per url in parallel")
	uploadTimes := flag.Int("upload-test-per-url", 8, "run upload test n times per url in parallel")

	maxTimeInTest := flag.Float64("test-time", 30, "time for each test")

	urlsToTest := flag.Int("url-to-test", 5, "number of urls to request from api")

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
	api_tr.Proxy = nil
	http.DefaultClient.Transport = api_tr

	resp, err := http.Get("https://api.fast.com/netflix/speedtest/v2?https=true&token=" + FastApiToken + "&urlCount=" + strconv.Itoa(*urlsToTest))
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
		// if totalTimes == 0, all goroutines are done
		totalTimes := 0
		// if totalTimes > uploadInitialSent, we shouldn't count and only increment
		uploadInitialSent := 0

		for _, i := range m["targets"].([]interface{}) {
			if test == "latency" {
				var testUrl = FormatFastURL(i.(map[string]interface{})["url"].(string), 0)
				for j := 0; j < *pingTimes; j++ {
					go PingURL(testUrl, doneChan, pingChan)
					totalTimes++
				}
			} else {
				var testUrl = FormatFastURL(i.(map[string]interface{})["url"].(string), FastMaxPayload)
				if test == "download" {
					for j := 0; j < *downTimes; j++ {
						go DownloadFile(testUrl, doneChan, *maxTimeInTest, currentSpeed)
						totalTimes++
					}
				}

				if test == "upload" {
					for j := 0; j < *uploadTimes; j++ {
						go UploadFile(testUrl, doneChan, *maxTimeInTest, currentSpeed, &uploadInitialSent)
						totalTimes++
					}
				}
			}
		}

		var startTime = time.Now()
		var totalDl = uint(0)
		var totalPing []float64
		var speedMbps = float64(0)

	outer:
		for {
			select {
			case c := <-currentSpeed:
				if test == "download" {
					totalDl += c
					speedMbps = calculateSpeed(float64(totalDl), startTime)
					if term.IsTerminal(syscall.Stdout) {
						fmt.Printf("\r\033[KDownload   %0.3f Mbps", speedMbps)
					}
				} else {
					// Don't count the first packets uploaded
					if uploadInitialSent > totalTimes {
						totalDl += c
						speedMbps = calculateSpeed(float64(totalDl), startTime)
						if term.IsTerminal(syscall.Stdout) {
							fmt.Printf("\r\033[KUpload     %0.3f Mbps", speedMbps)
						}
					} else {
						uploadInitialSent++
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
