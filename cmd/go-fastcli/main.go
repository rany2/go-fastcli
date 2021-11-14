package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"time"
)

const FastMaxPayload = 26214400
const FastAPIToken = "YXNkZmFzZGxmbnNkYWZoYXNkZmhrYWxm"
const FastAPIURL = "https://api.fast.com/netflix/speedtest/v2?https=true&token=" + FastAPIToken

var tr = &http.Transport{
	DisableCompression:  true,
	Proxy:               nil,
	DisableKeepAlives:   false,
	MaxIdleConnsPerHost: 1024,
}

var client = &http.Client{Transport: tr}

type LocationInfo struct {
	City    string
	Country string
}

type ConnectionInfo struct {
	ASN      string
	IP       string
	Location LocationInfo
}

type FastServer struct {
	City    string
	Country string
	URL     string
}

type FakeReader struct {
	ReadIndex int64
	MaxIndex  int64
}

func (c *FakeReader) Read(p []byte) (int, error) {
	if c.ReadIndex >= c.MaxIndex {
		return 0, io.EOF
	}
	for i := range p {
		p[i] = 0
	}
	n := len(p)
	c.ReadIndex += int64(n)
	return n, nil
}

func CalcMean(nums []float64) float64 {
	var total float64
	for _, num := range nums {
		total += num
	}
	return total / float64(len(nums))
}

func CalcMeanOfLastN(nums []float64, n int) float64 {
	if len(nums) < n {
		panic("Not enough numbers to calculate mean")
	} else if n <= 0 {
		panic("n must be greater than 0")
	} else if n > len(nums) {
		panic("n must be less than or equal to the number of numbers")
	} else {
		return CalcMean(nums[len(nums)-n:])
	}
}

func CalcStdDeviation(nums []float64) float64 {
	mean := CalcMean(nums)
	var total float64
	for _, num := range nums {
		total += math.Pow(num-mean, 2)
	}
	return math.Sqrt(total / float64(len(nums)))
}

func CalcStdDeviationLastN(nums []float64, n int) float64 {
	if len(nums) < n {
		panic("Not enough numbers to calculate std deviation")
	} else if n <= 0 {
		panic("n must be greater than 0")
	} else if n > len(nums) {
		panic("n must be less than or equal to the number of numbers")
	} else {
		return CalcStdDeviation(nums[len(nums)-n:])
	}
}

func CalcMaxValue(nums []float64) float64 {
	var max float64
	for _, num := range nums {
		if num > max {
			max = num
		}
	}
	return max
}

func CalcMaxValueLastN(nums []float64, n int) float64 {
	if len(nums) < n {
		panic("Not enough numbers to calculate max value")
	} else if n <= 0 {
		panic("n must be greater than 0")
	} else if n > len(nums) {
		panic("n must be less than or equal to the number of numbers")
	} else {
		return CalcMaxValue(nums[len(nums)-n:])
	}
}

func CalcJitter(nums []float64) float64 {
	if len(nums) < 2 {
		panic("Not enough numbers to calculate jitter")
	} else {
		diffs := float64(0)
		for i := 1; i < len(nums); i++ {
			diffs += math.Abs(nums[i] - nums[i-1])
		}
		return diffs / float64(len(nums)-1)
	}
}

func FormatFastURL(url string, rangeEnd int) string {
	return strings.Replace(url, "/speedtest?", fmt.Sprintf("/speedtest/range/0-%d?", rangeEnd), -1)
}

func FastGetServerList(urlsToTest int) (ConnectionInfo, []FastServer) {
	resp, err := client.Get(FastAPIURL + fmt.Sprintf("&urlCount=%d", urlsToTest))
	if err != nil {
		panic("Error getting server list")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic("Fast.com API returned " + resp.Status)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic("Error reading server list")
	}
	var jsonData map[string]interface{}
	if err := json.Unmarshal(body, &jsonData); err != nil {
		panic("Error parsing server list")
	}
	var connectionInfo ConnectionInfo
	var locationInfo LocationInfo
	var fastServer FastServer
	var fastServerList []FastServer
	var serverList = jsonData["targets"].([]interface{})
	for _, server := range serverList {
		serverData := server.(map[string]interface{})
		fastServer = FastServer{
			City:    serverData["location"].(map[string]interface{})["city"].(string),
			Country: serverData["location"].(map[string]interface{})["country"].(string),
			URL:     serverData["url"].(string),
		}
		fastServerList = append(fastServerList, fastServer)
	}
	connectionInfo.IP = jsonData["client"].(map[string]interface{})["ip"].(string)
	connectionInfo.ASN = jsonData["client"].(map[string]interface{})["asn"].(string)
	locationInfo.City = jsonData["client"].(map[string]interface{})["location"].(map[string]interface{})["city"].(string)
	locationInfo.Country = jsonData["client"].(map[string]interface{})["location"].(map[string]interface{})["country"].(string)
	connectionInfo.Location = locationInfo
	return connectionInfo, fastServerList
}

func GetHost(_url string) string {
	u, err := url.Parse(_url)
	if err != nil {
		panic("Error parsing URL")
	}
	return u.Host
}

func GetLatency(url string) time.Duration {
	req, err := http.NewRequest("HEAD", FormatFastURL(url, 0), nil)
	if err != nil {
		panic("Error creating request")
	}
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
	_, err = tr.RoundTrip(req)
	if err != nil {
		panic("Error making request")
	}
	return t2.Sub(t1)
}

func GetDownloadSpeed(url string, playloadSize int) float64 {
	resp, err := client.Get(FormatFastURL(url, playloadSize))
	if err != nil {
		panic("Error getting download speed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic("Fast.com API returned " + resp.Status)
	}
	t1 := time.Now()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		panic("Error reading download speed")
	}
	return float64(playloadSize) / time.Since(t1).Seconds()
}

func GetUploadSpeed(url string, playload_size int) float64 {
	counter := &FakeReader{
		ReadIndex: 0,
		MaxIndex:  int64(playload_size),
	}
	req, err := http.NewRequest("POST", FormatFastURL(url, playload_size), counter)
	if err != nil {
		panic("Error creating request")
	}
	req.ContentLength = int64(playload_size)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept-Encoding", "identity")

	t1 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		panic("Error doing request")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic("Fast.com API returned " + resp.Status)
	}
	return float64(int64(playload_size)) / time.Since(t1).Seconds()
}

func main() {
	// number of servers to request
	serverNum := 1

	// number of times to measure latency
	latencyLoopNum := 10

	// max loops to run
	downMaxLoop := 100

	// payload size
	downMeasureSlowMB := 2  // for slow connections
	downMeasureFastMB := 10 // for fast connections

	// any value over this would be considered a fast connection
	downMeasureCutoffMB := float64(downMeasureSlowMB)

	// take last n values to calculate standard deviation
	downStdLastVarsSlow := 3 // for slow connections
	downStdLastVarsFast := 4 // for fast connections

	// if standard deviation is less than this, we break out of the loop
	downStdMaxSlow := 0.2 // for slow connections
	downStdMaxFast := 5.0 // for fast connections

	// same as above, but for upload
	upMaxLoop := downMaxLoop
	upMeasureSlowMB := downMeasureSlowMB
	upMeasureFastMB := downMeasureFastMB
	upMeasureCutoffMB := downMeasureCutoffMB
	upStdLastVarsSlow := downStdLastVarsSlow
	upStdLastVarsFast := downStdLastVarsFast
	upStdMaxSlow := downStdMaxSlow
	upStdMaxFast := downStdMaxFast

	connectionInfo, fastServerList := FastGetServerList(serverNum)
	fmt.Println("Fast.com Speedtest")
	fmt.Println()
	fmt.Printf("Connection Info:\n")
	fmt.Printf("  - IP: %s\n", connectionInfo.IP)
	fmt.Printf("  - ASN: %s\n", connectionInfo.ASN)
	fmt.Printf("  - Location: %s, %s\n", connectionInfo.Location.City, connectionInfo.Location.Country)
	fmt.Println()
	fmt.Println("Fast.com Servers:")
	for _, server := range fastServerList {
		fmt.Printf("  - Location: %s, %s\n", server.City, server.Country)
		fmt.Printf("    URL: %s\n", server.URL)
		fmt.Println()
	}

	fmt.Println("Latency:")
	for _, server := range fastServerList {
		totalLatency := []time.Duration{}
		for i := 0; i < latencyLoopNum; i++ {
			totalLatency = append(totalLatency, GetLatency(server.URL))
		}
		fmt.Printf("  - %s: %0.3f ms (%0.3f ms jitter)\n",
			GetHost(server.URL),
			CalcMean(
				func() []float64 {
					var temp []float64
					for i := 0; i < len(totalLatency); i++ {
						temp = append(temp, float64(totalLatency[i].Nanoseconds()))
					}
					return temp
				}(),
			)*float64(time.Nanosecond)/float64(time.Millisecond),
			CalcJitter(
				func() []float64 {
					var temp []float64
					for i := 0; i < len(totalLatency); i++ {
						temp = append(temp, float64(totalLatency[i].Nanoseconds()))
					}
					return temp
				}(),
			)*float64(time.Nanosecond)/float64(time.Millisecond),
		)
	}
	fmt.Println()

	fmt.Println("Download Speed:")
	for _, server := range fastServerList {
		totalDownloads := []float64{}
		downMeasureMB := downMeasureSlowMB
		downStdLastVars := downStdLastVarsSlow
		downStdMax := downStdMaxSlow
		cutOffComplete := false
		for i := 0; i < downMaxLoop; i++ {
			downloadSpeed := GetDownloadSpeed(server.URL, downMeasureMB*1024*1024)
			if !cutOffComplete && downloadSpeed > downMeasureCutoffMB*1024*1024 {
				downMeasureMB = downMeasureFastMB
				downStdLastVars = downStdLastVarsFast
				downStdMax = downStdMaxFast
				cutOffComplete = true
				i-- // Retry this iteration
				continue
			}
			totalDownloads = append(totalDownloads, downloadSpeed)
			if len(totalDownloads) >= downStdLastVars && CalcStdDeviationLastN(totalDownloads, downStdLastVars) < 1024*1024*downStdMax {
				break
			}
		}
		fmt.Printf("  - %s: %0.3f Mbit/s (used %d MB)\n", GetHost(server.URL), CalcMaxValueLastN(totalDownloads, downStdLastVars)/125000, len(totalDownloads)*downMeasureMB)
	}
	fmt.Println()

	fmt.Println("Upload Speed:")
	for _, server := range fastServerList {
		totalUploads := []float64{}
		upMeasureMB := upMeasureSlowMB
		upStdLastVars := upStdLastVarsSlow
		upStdMax := upStdMaxSlow
		cutOffComplete := false
		for i := 0; i < upMaxLoop; i++ {
			uploadSpeed := GetUploadSpeed(server.URL, upMeasureMB*1024*1024)
			if !cutOffComplete && uploadSpeed > upMeasureCutoffMB*1024*1024 {
				upMeasureMB = upMeasureFastMB
				upStdLastVars = upStdLastVarsFast
				upStdMax = upStdMaxFast
				cutOffComplete = true
				i-- // Retry this iteration
				continue
			}
			totalUploads = append(totalUploads, uploadSpeed)
			if len(totalUploads) >= upStdLastVars && CalcStdDeviationLastN(totalUploads, upStdLastVars) < 1024*1024*upStdMax {
				break
			}
		}
		fmt.Printf("  - %s: %0.3f Mbit/s (used %d MB)\n", GetHost(server.URL), CalcMaxValueLastN(totalUploads, upStdLastVars)/125000, len(totalUploads)*upMeasureMB)
	}
}
