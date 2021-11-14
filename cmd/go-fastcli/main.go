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

func Mean(nums []float64) float64 {
	var total float64
	for _, num := range nums {
		total += num
	}
	return total / float64(len(nums))
}

func StdDeviation(nums []float64) float64 {
	mean := Mean(nums)
	var total float64
	for _, num := range nums {
		total += math.Pow(num-mean, 2)
	}
	return math.Sqrt(total / float64(len(nums)))
}

func StdDeviationLastN(nums []float64, n int) float64 {
	if len(nums) < n {
		panic("Not enough numbers to calculate std deviation")
	} else if n <= 0 {
		panic("n must be greater than 0")
	} else if n > len(nums) {
		panic("n must be less than or equal to the number of numbers")
	} else {
		return StdDeviation(nums[len(nums)-n:])
	}
}

func GetMaxValue(nums []float64) float64 {
	var max float64
	for _, num := range nums {
		if num > max {
			max = num
		}
	}
	return max
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
	serverNum := 1

	downMaxLoop := 100
	downMeasureMB := 1
	downStdLastVars := 5
	downStdMax := 0.9

	upMaxLoop := 100
	upMeasureMB := 1
	upStdLastVars := 5
	upStdMax := 0.9

	connectionInfo, fastServerList := FastGetServerList(serverNum)
	fmt.Println("Fast.com Speedtest")
	fmt.Println()
	fmt.Println("Connection Info:")
	fmt.Println("  IP: " + connectionInfo.IP)
	fmt.Println("  ASN: " + connectionInfo.ASN)
	fmt.Println("  Location: " + connectionInfo.Location.City + ", " + connectionInfo.Location.Country)
	fmt.Println()
	fmt.Println("Fast.com Servers:")
	for _, server := range fastServerList {
		fmt.Println("  Location: " + server.City + ", " + server.Country)
		fmt.Println("  URL: " + server.URL)
		fmt.Println()
	}

	fmt.Println("Latency:")
	for _, server := range fastServerList {
		latency := GetLatency(server.URL)
		fmt.Printf("  %s: %dms\n", GetHost(server.URL), latency.Milliseconds())
	}
	fmt.Println()

	fmt.Println("Download Speed:")
	for _, server := range fastServerList {
		var downloadSpeed float64
		totalDownloads := []float64{}
		for i := 0; i < downMaxLoop; i++ {
			downloadSpeed = GetDownloadSpeed(server.URL, downMeasureMB*1024*1024)
			totalDownloads = append(totalDownloads, downloadSpeed)
			if len(totalDownloads) >= downStdLastVars && StdDeviationLastN(totalDownloads, downStdLastVars) < 1024*1024*downStdMax {
				fmt.Println("HERE")
				break
			}
		}
		//fmt.Printf("  %s: %0.3fMB/s\n", GetHost(server.URL), GetMaxValue(totalDownloads)/1024/1024)
		fmt.Printf("  %s: %0.3fMbit/s\n", GetHost(server.URL), GetMaxValue(totalDownloads)/125000)
	}
	fmt.Println()

	fmt.Println("Upload Speed:")
	for _, server := range fastServerList {
		var uploadSpeed float64
		totalUploads := []float64{}
		for i := 0; i < upMaxLoop; i++ {
			uploadSpeed = GetUploadSpeed(server.URL, upMeasureMB*1024*1024)
			totalUploads = append(totalUploads, uploadSpeed)
			if len(totalUploads) >= upStdLastVars && StdDeviationLastN(totalUploads, upStdLastVars) < 1024*1024*upStdMax {
				fmt.Println("HERE")
				break
			}
		}
		//fmt.Printf("  %s: %0.3fMB/s\n", GetHost(server.URL), GetMaxValue(totalUploads)/1024/1024)
		fmt.Printf("  %s: %0.3fMbit/s\n", GetHost(server.URL), GetMaxValue(totalUploads)/125000)
	}
}
