package main

import (
	"fmt"
	"net/http"
	"log"
	"io/ioutil"
	"encoding/json"
	"strings"
	"bufio"
	"time"
	"context"
	"errors"
	"bytes"
	"flag"
	"strconv"
)

// Create HTTP transports to share pool of connections while disabling compression
var tr = &http.Transport{DisableCompression: true,}
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

// To announce the death of the goroutine
func announce_death(done chan bool) {
	done <- true
}

func download_file(url string, done chan bool, timeout int64, speed chan uint) {
	// Send done to channel on death of function
	defer announce_death(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout) * time.Second)

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

func upload_file(url string, done chan bool, timeout int64, speed chan uint) {
	// Send done to channel on death of function
	defer announce_death(done)

	// Create context with time limit
	ctx := context.Background()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout) * time.Second)

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
			count, err := reader.Read(part)
			if err != nil {
				break
			} else {
				log.Fatalf ("\nUpload server never returns anything!\n%s\n", part[:count])
			}
		}
	}
}



func main() {
	downloadBool := flag.Bool("download", false, "run download test")
	uploadBool := flag.Bool("upload", false, "run upload test")
	parallelPerURL := flag.Int("test_per_url", 2, "run test x times per url")
	maxTimeInTest := flag.Int64("test_time", 30, "total time for each test")
	urlsToTest := flag.Int("url_to_test", 5, "number of urls to get from api")
	flag.Parse()

	testsToRun := []string{}
	if !*downloadBool && !*uploadBool {
		*downloadBool = true
		*uploadBool = true
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
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal (err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &m); err != nil {
			log.Fatal (err)
		}

		currentSpeed := make(chan uint, 1)
		doneChan := make(chan bool, 1)
		for _, test := range testsToRun {
		//for _, test := range []string{"upload"} {
			totalTimes := 0 // to detect when all download goroutines are done
			for _, i := range m["targets"].([]interface{}) {
				var testUrl = strings.Replace(i.(map[string]interface{})["url"].(string), "/speedtest?", "/speedtest/range/0-26214400?", -1)
				for j := 0; j < *parallelPerURL; j++ { // 8 parallel for now
					if test == "download" {
						go download_file (testUrl, doneChan, *maxTimeInTest, currentSpeed)
					} else {
						go upload_file (testUrl, doneChan, *maxTimeInTest, currentSpeed)
					}
					totalTimes++
				}
			}

			var startTime = time.Now().Unix()
			var totalDl = uint(0)
			outer:
				for {
					select {
					case c := <- currentSpeed:
						totalDl += c
						var speedMbps = float64(totalDl)/float64(time.Now().Unix() - startTime)/125000
						if test == "download" {
							fmt.Printf ("\r\033[KDownload: %0.3f Mbps", speedMbps)
						} else {
							fmt.Printf ("\r\033[KUpload: %0.3f Mbps", speedMbps)
						}
					case <- doneChan:
						totalTimes--
						if totalTimes == 0 {
							break outer
						}
					default:
						time.Sleep(1 * time.Millisecond)
					}
				}
			fmt.Println()
		}
	}
}